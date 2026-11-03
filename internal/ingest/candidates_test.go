package ingest

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"congress-visualizer/internal/database"
)

// Candidate lifecycle regression tests (docs/DESIGN.md Drain contract):
// pending -> fetched on hit OR miss (stamped BY IDENTITY, never batch-wide);
// error -> attempts++/last_error; attempts >= 5 quarantines (excluded from
// pending, surfaced in the result). A non-numeric external_id is a failure
// toward quarantine, never a silent skip.
//
// They need a migrated fixture DB (internal/database.Open + the goose
// migrations) and a stub unit of work — the drain owns no state, it applies
// outcomes; *Syncer satisfies the seam in production.

// stubSyncer fakes SyncVotationID: outcome per external ID; anything absent
// returns the zero Outcome — exactly what SyncVotationID reports for a
// transport/decode/upsert failure.
type stubSyncer struct{ outcomes map[int64]Outcome }

func (s stubSyncer) SyncVotationID(_ context.Context, id int64) Outcome {
	return s.outcomes[id]
}

func testQueue(t *testing.T) (candidateQueue, *sql.DB) {
	t.Helper()
	odb, err := database.Open(filepath.Join(t.TempDir(), "candidates.db"))
	if err != nil {
		t.Fatalf("open fixture db: %v", err)
	}
	t.Cleanup(func() { odb.Close() })
	if err := odb.Migrate("../../db/migrations"); err != nil {
		t.Fatalf("migrate fixture db: %v", err)
	}
	db := odb.Raw()
	return candidateQueue{
		q:   database.New(db),
		log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, db
}

// seedCandidate inserts a pending candidate with an explicit discovered_at so
// drain order is deterministic.
func seedCandidate(t *testing.T, db *sql.DB, externalID, discovered string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO votation_candidates
		(chamber_id, external_id, source, discovered_at)
		VALUES ('camara', ?, 'test', ?)`, externalID, discovered); err != nil {
		t.Fatalf("seed candidate %s: %v", externalID, err)
	}
}

func candidateRow(t *testing.T, db *sql.DB, externalID string) database.VotationCandidate {
	t.Helper()
	var c database.VotationCandidate
	err := db.QueryRow(`SELECT chamber_id, external_id, source, discovered_at, fetched_at, attempts, last_error
		FROM votation_candidates WHERE chamber_id = 'camara' AND external_id = ?`, externalID).
		Scan(&c.ChamberID, &c.ExternalID, &c.Source, &c.DiscoveredAt, &c.FetchedAt, &c.Attempts, &c.LastError)
	if err != nil {
		t.Fatalf("read candidate %s: %v", externalID, err)
	}
	return c
}

func pendingCount(t *testing.T, q *database.Queries) int {
	t.Helper()
	n, err := q.CountPendingCandidates(context.Background(), int64(maxAttempts))
	if err != nil {
		t.Fatalf("count pending: %v", err)
	}
	return int(n)
}

func drainOnce(t *testing.T, cq candidateQueue, vs votationSyncer, limit int) DrainResult {
	t.Helper()
	res, err := cq.drain(context.Background(), vs, limit)
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	return res
}

// Regression for the batch-wide stamp: Drain(limit=1) with one miss must
// stamp ONLY the row it drained — "3 pending, 1 miss -> 2 still pending".
// (The old call, MarkCandidateFetched() with no key arg, stamped every other
// pending candidate's fetched_at too, silently dropping discovery data.)
func TestDrainMissStampsOnlyTheDrainedRow(t *testing.T) {
	cq, db := testQueue(t)
	seedCandidate(t, db, "42001", "2026-01-01")
	seedCandidate(t, db, "42002", "2026-01-02")
	seedCandidate(t, db, "42003", "2026-01-03")

	res := drainOnce(t, cq, stubSyncer{map[int64]Outcome{
		42001: {Miss: true}, 42002: {Miss: true}, 42003: {Miss: true},
	}}, 1)

	if res.Drained != 1 || res.Misses != 1 {
		t.Fatalf("DrainResult = %+v, want Drained 1, Misses 1", res)
	}
	if got := pendingCount(t, cq.q); got != 2 {
		t.Fatalf("pending = res %d / db %d, want 2 (one miss must not sweep the queue)", res.Pending, got)
	}
	if row := candidateRow(t, db, "42001"); !row.FetchedAt.Valid {
		t.Fatalf("first candidate 42001 not stamped: %+v", row)
	}
	for _, ext := range []string{"42002", "42003"} {
		row := candidateRow(t, db, ext)
		if row.FetchedAt.Valid || row.Attempts != 0 {
			t.Fatalf("untouched candidate %s mutated: fetched=%v attempts=%d", ext, row.FetchedAt.Valid, row.Attempts)
		}
	}
}

// Regression for the never-stamped hit: a successful drain must stamp
// fetched_at (by identity), or the row re-syncs every Tick forever.
func TestDrainHitMarksRowFetched(t *testing.T) {
	cq, db := testQueue(t)
	seedCandidate(t, db, "42001", "2026-01-01")

	res := drainOnce(t, cq, stubSyncer{map[int64]Outcome{42001: {Hit: true, VotationID: 77}}}, 50)

	if res.Hits != 1 || res.Drained != 1 {
		t.Fatalf("DrainResult = %+v, want Drained 1, Hits 1", res)
	}
	if got := pendingCount(t, cq.q); got != 0 {
		t.Fatalf("pending = res %d / db %d, want 0 (hit must stamp the row)", res.Pending, got)
	}
	if row := candidateRow(t, db, "42001"); !row.FetchedAt.Valid || row.Attempts != 0 {
		t.Fatalf("hit row not stamped cleanly: attempts=%d fetched=%v", row.Attempts, row.FetchedAt.Valid)
	}
}

// Regression for the orphaned FailCandidate: an error outcome bumps
// attempts/last_error, and attempts >= maxAttempts quarantines the row —
// excluded from pending and surfaced in the result.
func TestDrainErrorsQuarantine(t *testing.T) {
	cq, db := testQueue(t)
	seedCandidate(t, db, "42001", "2026-01-01")

	var qn []database.VotationCandidate
	for i := 1; i <= maxAttempts; i++ {
		res := drainOnce(t, cq, stubSyncer{}, 50) // every fetch fails
		row := candidateRow(t, db, "42001")
		if row.Attempts != int64(i) {
			t.Fatalf("attempt %d: attempts = %d, want %d", i, row.Attempts, i)
		}
		if !row.LastError.Valid || row.LastError.String == "" {
			t.Fatalf("attempt %d: last_error must be recorded, got %v", i, row.LastError)
		}
		if i < maxAttempts && len(res.Quarantined) != 0 {
			t.Fatalf("attempt %d: not yet quarantined, got %+v", i, res.Quarantined)
		}
		if i < maxAttempts && pendingCount(t, cq.q) != 1 {
			t.Fatalf("attempt %d: row must stay pending, got %d", i, pendingCount(t, cq.q))
		}
		qn = res.Quarantined
	}
	if len(qn) != 1 || qn[0].ExternalID != "42001" {
		t.Fatalf("quarantine not surfaced in the result: %+v", qn)
	}
	if got := pendingCount(t, cq.q); got != 0 {
		t.Fatalf("quarantined row still counts as pending: %d", got)
	}
}

// A non-numeric external_id is a data defect: it counts as a failure toward
// quarantine instead of the old silent skip (fmt.Sscan parsed "abc" to 0,
// continue'd, and the row was retried — or dropped — forever).
func TestDrainNonNumericExternalIDCountsAsFailure(t *testing.T) {
	cq, db := testQueue(t)
	seedCandidate(t, db, "camara-boletin-4210", "2026-01-01")

	var qn []database.VotationCandidate
	for i := 1; i <= maxAttempts; i++ {
		res := drainOnce(t, cq, stubSyncer{map[int64]Outcome{42001: {Miss: true}}}, 50)
		row := candidateRow(t, db, "camara-boletin-4210")
		if row.Attempts != int64(i) {
			t.Fatalf("attempt %d: attempts = %d, want %d", i, row.Attempts, i)
		}
		if i == 1 && row.FetchedAt.Valid {
			t.Fatalf("non-numeric row was stamped fetched: %+v", row)
		}
		qn = res.Quarantined
	}
	if len(qn) == 0 || pendingCount(t, cq.q) != 0 {
		t.Fatalf("non-numeric row never quarantined: res %+v pending %d", qn, pendingCount(t, cq.q))
	}
}

// EnqueueCandidate is INSERT ... ON CONFLICT DO NOTHING over the
// (chamber_id, external_id) key: re-enqueues and re-drains must not duplicate
// rows or resurrect a fetched candidate.
func TestDrainDuplicateIdempotent(t *testing.T) {
	cq, db := testQueue(t)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if err := cq.q.EnqueueCandidate(ctx, database.EnqueueCandidateParams{
			ChamberID: "camara", ExternalID: "42001", Source: "test",
		}); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}
	drainOnce(t, cq, stubSyncer{map[int64]Outcome{42001: {Miss: true}}}, 50)

	var n int
	if err := db.QueryRow(`SELECT count(*) FROM votation_candidates WHERE chamber_id = 'camara' AND external_id = '42001'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("duplicated rows: %d", n)
	}
	second := drainOnce(t, cq, stubSyncer{map[int64]Outcome{42001: {Miss: true}}}, 50)
	if second.Drained != 0 || pendingCount(t, cq.q) != 0 {
		t.Fatalf("re-drain resurrected work: %+v pending %d", second, pendingCount(t, cq.q))
	}
}

// Drain reports the post-drain pending count for the sync result.
func TestDrainSurfacesPendingCount(t *testing.T) {
	cq, db := testQueue(t)
	seedCandidate(t, db, "42001", "2026-01-01")
	seedCandidate(t, db, "42002", "2026-01-02")

	res := drainOnce(t, cq, stubSyncer{map[int64]Outcome{
		42001: {Miss: true}, 42002: {Miss: true},
	}}, 50)

	if got := pendingCount(t, cq.q); got != 0 {
		t.Fatalf("pending = res %d / db %d, want 0", res.Pending, got)
	}
	if res.Drained != 2 || res.Misses != 2 {
		t.Fatalf("DrainResult = %+v, want Drained 2, Misses 2", res)
	}
}
