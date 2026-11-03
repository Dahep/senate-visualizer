package ingest

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"congress-visualizer/internal/database"
	"congress-visualizer/internal/ingest/camara"
)

// Deputy-roster regression tests. The roster pass is ONE transaction (the
// deactivate-all + grant-per-vigente body): a mid-pass failure rolls back to
// the previous roster instead of leaving every remaining deputy inactive and
// the pages rendering mass "Histórico" until the next tick.

func deputy(t *testing.T, db *sql.DB, externalID string, active bool) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO representatives
		(chamber_id, external_id, first_name, last_name, active)
		VALUES ('camara', ?, 'Old', 'Name', ?)`, externalID, active); err != nil {
		t.Fatalf("seed deputy %s: %v", externalID, err)
	}
}

func deputyActive(t *testing.T, db *sql.DB, externalID string) int {
	t.Helper()
	var active int
	if err := db.QueryRow(`SELECT active FROM representatives
		WHERE chamber_id = 'camara' AND external_id = ?`, externalID).Scan(&active); err != nil {
		t.Fatalf("read deputy %s: %v", externalID, err)
	}
	return active
}

func dip(id int) camara.DiputadoXML {
	return camara.DiputadoXML{
		DIPID: id, Nombre: "New", ApellidoPaterno: "Name",
		Sexo: camara.CodedText{Code: "1", Value: "M"}, FechaNacimiento: "1970-01-01T00:00:00",
	}
}

// Membership IS active: everyone not in the vigentes set flips to inactive,
// everyone in it is upserted and granted — including a reactivated former
// deputy returning to the chamber.
func TestSyncDeputiesRosterFollowsVigentes(t *testing.T) {
	s, db := newTestSyncer(t)
	deputy(t, db, "100", true)  // leaves the chamber
	deputy(t, db, "101", false) // re-enters it
	deputy(t, db, "200", true)  // stays a member
	s.client = stubAPIClient{vig: []camara.DiputadoXML{dip(101), dip(200)}}

	n, err := s.SyncDeputies(context.Background())
	if err != nil {
		t.Fatalf("SyncDeputies: %v", err)
	}
	if n != 0 {
		t.Fatalf("enrichment count = %d, want 0 (no all-history rows)", n)
	}
	if got := deputyActive(t, db, "100"); got != 0 {
		t.Fatalf("non-member 100 active=%d, want 0", got)
	}
	if got := deputyActive(t, db, "101"); got != 1 {
		t.Fatalf("re-entering member 101 active=%d, want 1", got)
	}
	if got := deputyActive(t, db, "200"); got != 1 {
		t.Fatalf("member 200 active=%d, want 1", got)
	}
}

// The all-history pass enriches and never un-grants active (upserts only,
// outside the roster transaction).
func TestSyncDeputiesEnrichesWithoutFlippingActive(t *testing.T) {
	s, db := newTestSyncer(t)
	deputy(t, db, "200", true)
	s.client = stubAPIClient{
		vig: []camara.DiputadoXML{dip(200)},
		all: []camara.DiputadoXML{dip(100), dip(200)},
	}

	if n, err := s.SyncDeputies(context.Background()); err != nil || n != 2 {
		t.Fatalf("SyncDeputies: n=%d err=%v", n, err)
	}
	var gender, birth string
	if err := db.QueryRow(`SELECT gender, birth_date FROM representatives
		WHERE chamber_id='camara' AND external_id='100'`).Scan(&gender, &birth); err != nil {
		t.Fatalf("read enriched deputy: %v", err)
	}
	if gender != "M" || birth != "1970-01-01" {
		t.Fatalf("enrichment = %q/%q, want M/1970-01-01", gender, birth)
	}
	if got := deputyActive(t, db, "100"); got != 0 {
		t.Fatalf("enriched non-member 100 active=%d, want 0", got)
	}
	if got := deputyActive(t, db, "200"); got != 1 {
		t.Fatalf("enriched member 200 active=%d, want 1", got)
	}
}

// Catch-the-old-bug: a DB failure between the deactivate-all and the last
// grant must leave the PREVIOUS roster intact — SyncDeputies must run the
// pass through its transaction, so the failure rolls everything back. The
// failure is injected on the second upsert inside the pass (real Syncer, an
// already-canceled statement context).
func TestSyncDeputiesMidPassFailureRollsBack(t *testing.T) {
	s, db := newTestSyncer(t)
	deputy(t, db, "100", true)
	deputy(t, db, "101", false)
	deputy(t, db, "999", true)
	// Deputy 999 is NOT in vigentes: a committed pass would deactivate it
	// and a rolled-back pass must keep it active — the observable difference.
	s.client = stubAPIClient{vig: []camara.DiputadoXML{dip(100), dip(101)}}
	s.rosterQueries = func(tx *sql.Tx) *database.Queries {
		return database.New(&failRowDBTX{DBTX: tx, failAt: 2}) // 2nd row-returning stmt = the 2nd upsert
	}

	_, err := s.SyncDeputies(context.Background())
	if err == nil {
		t.Fatal("injected mid-pass failure did not fire")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("injected failure lost: %v", err)
	}
	for id, want := range map[string]int{"100": 1, "101": 0, "999": 1} {
		if got := deputyActive(t, db, id); got != want {
			t.Fatalf("deputy %s active=%d, want %d — the tx did not keep the old roster", id, got, want)
		}
	}
}

// failRowDBTX fails the Nth row-returning statement with a canceled
// statement context: a deterministic, driver-agnostic mid-tx error.
type failRowDBTX struct {
	database.DBTX
	failAt int
	n      int
}

func (f *failRowDBTX) QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row {
	f.n++
	if f.n == f.failAt {
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		return f.DBTX.QueryRowContext(cctx, query, args...)
	}
	return f.DBTX.QueryRowContext(ctx, query, args...)
}

var _ database.DBTX = (*failRowDBTX)(nil)
