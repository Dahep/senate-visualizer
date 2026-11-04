// Package ingest is the ONLY write path into ingested tables (docs/DESIGN.md
// gap 6): one Syncer, three interchangeable drivers — cmd/sync -full :
// Scan(Down), cmd/sync -once / server scheduler — Tick. All share cursors in
// sync_metadata; a single-writer lease (camara.sync_lock) guarantees one
// process owns the sweep state and the shared 150 ms rate limiter.
package ingest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"congress-visualizer/internal/database"
	"congress-visualizer/internal/ingest/camara"
)

// sync_metadata keys.
const (
	keyHighWater  = "camara.high_water"  // max external votation ID ever SWEPT
	keyFloor      = "camara.floor"       // bisected lower bound of the ID space
	keyScanCursor = "camara.scan_cursor" // durable sweep resume point
	keySyncLock   = "camara.sync_lock"   // lease "<owner>|<unix expiry>"
)

const (
	gapTolerance = 50    // consecutive misses ending a sweep leg before probe-ahead widens
	seedID       = 87460 // live-verified current-window ID; sweeps grow from here
)

// Driver identities (docs/DESIGN.md gap 6): the two drivers pass theirs to
// NewSyncer and it becomes the lease owner — no owner strings at call sites.
const (
	DriverCmdSync   = "cmd/sync"
	DriverScheduler = "scheduler"
)

// Lease pacing. The lease is short-TTL and RENEWED while the run is alive, so
// a run holds it for its real duration: docs/DESIGN.md budgets the historical
// scan at ~3.6h (~87k requests), longer than any fixed expiry could honor.
// The SQL CAS steals only after the expiry passes, so three skipped renewals
// still cost nothing before a third party can take over.
const (
	leaseTTL     = 15 * time.Minute
	leaseRenewIn = 5 * time.Minute // renewal cadence, well inside the CAS steal window
)

// Outcome reports what one SyncVotationID did.
type Outcome struct {
	Miss       bool  // external ID does not exist (xsi:nil sentinel)
	Hit        bool  // votation decoded and persisted
	VotationID int64 // internal row id when Hit
}

// ScanReason documents WHY a sweep terminated. "Stopped in a gap" and
// "reached the floor" must be distinguishable; silent truncation is the
// failure mode the scan contract exists to prevent.
type ScanReason string

const (
	ReasonFloor     ScanReason = "floor"
	ReasonTolerance ScanReason = "tolerance"
	ReasonCtxDone   ScanReason = "context"
)

// ScanResult is the sweep outcome — including the depth check:
// MinVoteDate lets the operator see at a glance whether the scan actually
// reached history rather than stopping a week back. Pending and Quarantined
// summarize the last queue drain (Tick): pending EXCLUDES quarantined rows.
type ScanResult struct {
	EndReason   ScanReason
	Hits        int
	Misses      int
	GapRuns     []int // gap runs the sweep crossed (widths)
	Pending     int   // out-of-band candidates still pending (quarantined excluded)
	Quarantined int   // rows that crossed the maxAttempts threshold in the last drain
	MinVoteDate string
	// AffiliationGaps counts deputies WITH at least one vote the
	// party-at-date rule (rep_party_at_date) does not cover - the operator
	// signal DESIGN.md 5/graft C promises the sync result. Surfaced, never
	// an ingest failure: the human extends data/affiliations.json.
	AffiliationGaps int
}

func (r ScanResult) Log(l *slog.Logger, dir string) {
	l.Info("scan done", "dir", dir, "end_reason", r.EndReason,
		"hits", r.Hits, "misses", r.Misses, "min_vote_date", r.MinVoteDate,
		"candidates_pending", r.Pending, "candidates_quarantined", r.Quarantined,
		"affiliation_gaps", r.AffiliationGaps)
}

// Syncer is the one write path into ingested tables.
type Syncer struct {
	db     *sql.DB // transaction boundary for the per-votation unit of work
	q      *database.Queries
	cands  candidateQueue // votation_candidates lifecycle owner
	client camaraAPI
	log    *slog.Logger

	driver       string        // lease owner identity, set by the driver at construction
	leaseTTL     time.Duration // NewSyncer pins the prod values; tests override
	leaseRenewIn time.Duration

	// rosterQueries scopes the roster transaction's statements. Production:
	// q.WithTx(tx) — statements bind to the pass transaction. Tests
	// substitute a statement-failing wrapper to prove the rollback keeps the
	// previous roster (the mid-pass failure runs through SyncDeputies' real
	// tx wiring, not past it).
	rosterQueries func(tx *sql.Tx) *database.Queries
}

// camaraAPI is the slice of the Camara client the Syncer consumes. The
// production *camara.Client satisfies it; tests stub it.
type camaraAPI interface {
	VotacionDetalle(ctx context.Context, id int) (camara.VotacionXML, error)
	BoletinVotaciones(ctx context.Context, boletin string) ([]camara.VotacionXML, error)
	DiputadosVigentes(ctx context.Context) ([]camara.DiputadoXML, error)
	Diputados(ctx context.Context) ([]camara.DiputadoXML, error)
}

func NewSyncer(db *sql.DB, q *database.Queries, c *camara.Client, l *slog.Logger, driver string) *Syncer {
	if l == nil {
		l = slog.Default()
	}
	if strings.TrimSpace(driver) == "" {
		panic("ingest: driver identity required (DriverCmdSync | DriverScheduler)")
	}
	return &Syncer{
		db: db, q: q, cands: candidateQueue{q: q, log: l}, client: c, log: l,
		driver:        driver,
		leaseTTL:      leaseTTL,
		leaseRenewIn:  leaseRenewIn,
		rosterQueries: func(tx *sql.Tx) *database.Queries { return q.WithTx(tx) },
	}
}

// --- Cursor helpers ----------------------------------------------------------

func (s *Syncer) cursorInt(ctx context.Context, key string) (int64, error) {
	v, err := s.q.GetSyncValue(ctx, key)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return 0, nil
	case err != nil:
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(v), 10, 64) // cursors are int64; TEXT ids are never compared lexically
}

func (s *Syncer) setCursorInt(ctx context.Context, key string, v int64) error {
	return s.q.UpsertSyncValue(ctx, database.UpsertSyncValueParams{Key: key, Value: strconv.FormatInt(v, 10)})
}

// --- Single-writer lease ------------------------------------------------------

// ErrLeaseHeld is returned (wrapped) by Scan/Tick when another sweep owns
// sync_lock: the drivers treat it as a skip, not a failure.
var ErrLeaseHeld = errors.New("another sweep holds " + keySyncLock)

// AcquireSyncLock claims the lease (owner + time.Now()+leaseTTL). The SQL CAS
// succeeds when the lease is unclaimed, expired, or ours (same-owner CAS =
// renewal while still live); false = someone else's live lease holds it.
func (s *Syncer) AcquireSyncLock(ctx context.Context, owner string) (bool, error) {
	value := fmt.Sprintf("%s|%d", owner, time.Now().Add(s.leaseTTL).Unix())
	n, err := s.q.AcquireSyncLock(ctx, database.AcquireSyncLockParams{Value: value, Value_2: owner})
	return n > 0, err
}

func ownerIsh(current, owner string) bool {
	v, _, _ := strings.Cut(current, "|")
	return v == owner
}

// ReleaseSyncLock drops an owned lease.
func (s *Syncer) ReleaseSyncLock(ctx context.Context, owner string) error {
	v, err := s.q.GetSyncValue(ctx, keySyncLock)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // nothing to release
	} else if err == nil && ownerIsh(v, owner) {
		return s.q.ReleaseSyncLock(ctx)
	}
	return err
}

// syncLease is a held lease: the renewal loop's handles.
type syncLease struct {
	stop context.CancelFunc
	done chan struct{}
}

// holdLease acquires the single-writer lease for this Syncer's driver and
// keeps it renewed every leaseRenewIn until release: a run holds the lock for
// its REAL duration, and a third party can steal it only after the run stops
// renewing (the SQL CAS fires only on an expired lease).
func (s *Syncer) holdLease(ctx context.Context) (*syncLease, error) {
	ok, err := s.AcquireSyncLock(ctx, s.driver)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrLeaseHeld
	}
	// The renewal loop outlives caller cancellation: releasing must not
	// depend on a context that may already be done.
	lctx, cancel := context.WithCancel(context.Background())
	l := &syncLease{stop: cancel, done: make(chan struct{})}
	go func() {
		defer close(l.done)
		t := time.NewTicker(s.leaseRenewIn)
		defer t.Stop()
		for {
			select {
			case <-lctx.Done():
				return
			case <-t.C:
				if _, err := s.AcquireSyncLock(lctx, s.driver); err != nil {
					// Not fatal: the next tick retries, and the CAS steals
					// only after leaseTTL of uninterrupted failure.
					s.log.Error("lease renewal failed", "driver", s.driver, "err", err)
				}
			}
		}
	}()
	return l, nil
}

// release stops the renewal loop and drops the lease (no-op if it was lost).
func (s *Syncer) release(l *syncLease) {
	if l == nil {
		return
	}
	l.stop()
	<-l.done
	_ = s.ReleaseSyncLock(context.Background(), s.driver)
}

// --- The unit of work ---------------------------------------------------------

// SyncVotationID is THE unit of work: idempotent, one transaction, crash-safe.
// It NEVER advances cursor keys — only the sequential scanner does. (A drained
// queue candidate may sit far above the unswept frontier; advancing high_water
// from it would mark thousands of unprobed IDs as covered forever.)
func (s *Syncer) SyncVotationID(ctx context.Context, externalID int64) Outcome {
	x, err := s.client.VotacionDetalle(ctx, int(externalID))
	if errors.Is(err, camara.ErrNotFound) {
		return Outcome{Miss: true}
	}
	if err != nil {
		s.log.Warn("detail fetch failed", "id", externalID, "err", err)
		return Outcome{}
	}
	d, err := camara.ParseVotation(x)
	if err != nil {
		s.log.Warn("decode failed", "id", externalID, "err", err)
		return Outcome{}
	}
	o := s.upsertVotation(ctx, d)
	o.Hit = o.VotationID != 0
	return o
}

// upsertVotation persists one normalized votation atomically:
// session, bill stub, votation row, delete-and-replace votes, pareo
// overrides, and the audit totals cross-check — all in one tx.
func (s *Syncer) upsertVotation(ctx context.Context, d camara.VotationData) (outcome Outcome) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		s.fail("begin", d.ExternalID, err)
		return Outcome{}
	}
	defer tx.Rollback()
	q := s.q.WithTx(tx)

	var sessionID sql.NullInt64
	if d.Session.ID != 0 {
		num, _ := strconv.Atoi(strings.TrimSpace(d.Session.Numero))
		id, err := q.UpsertSession(ctx, database.UpsertSessionParams{
			ChamberID:  "camara",
			ExternalID: strconv.Itoa(d.Session.ID),
			Number:     int64(num),
			Date:       d.Session.Fecha,
			Type:       nullIfEmpty(d.Session.Tipo.Value),
		})
		if err != nil {
			s.fail("session", d.ExternalID, err)
			return outcome
		}
		sessionID = sql.NullInt64{Int64: id, Valid: true}
	}

	var billID sql.NullString
	if d.Boletin != "" {
		title := d.Subject
		nameplate := strings.TrimSpace(title) == "" // no Articulo: stand-in nameplate
		if nameplate {
			title = billNameplate(d.Boletin)
		}
		// Title upgrade is decided INSIDE the ingest tx by re-reading the
		// existing row (recency rule + manual/enriched protection; see
		// applyBillTitle) — never buried in the SQL's ON CONFLICT.
		if err := applyBillTitle(ctx, q, d.Boletin, billTitle{
			title:     title,
			titleDate: nullIfEmpty(d.VoteDate),
			nameplate: nameplate,
		}); err != nil {
			s.fail("bill title", d.ExternalID, err)
			return outcome
		}
		billID = sql.NullString{String: d.Boletin, Valid: true}
	}

	vID, err := q.UpsertVotation(ctx, database.UpsertVotationParams{
		ChamberID: "camara", ExternalID: strconv.Itoa(d.ExternalID),
		SessionID: sessionID, BillID: billID,
		Date: d.Date, VoteDate: d.VoteDate,
		Subject:         nullIfEmpty(d.Subject),
		VoteType:        nullIfEmpty(d.VoteType),
		Result:          nullIfEmpty(d.Result),
		QuorumType:      nullIfEmpty(d.Quorum),
		LegislativeStep: nullIfEmpty(d.Step),
		TotalYes:        int64(d.Totals.Yes),
		TotalNo:         int64(d.Totals.No),
		TotalAbstain:    int64(d.Totals.Abstain),
		TotalDispensed:  int64(d.Totals.Dispensed),
	})
	if err != nil {
		s.fail("votation", d.ExternalID, err)
		return outcome
	}

	if err := q.DeleteVotesByVotation(ctx, vID); err != nil {
		s.fail("delete votes", d.ExternalID, err)
		return outcome
	}
	var cast int
	for _, v := range d.Votes {
		rid, err := s.resolveRep(ctx, q, v.Deputy)
		if err != nil {
			s.fail("rep", d.ExternalID, err)
			return outcome
		}
		if err := q.InsertVote(ctx, database.InsertVoteParams{
			VotationID: vID, RepresentativeID: rid, Vote: v.Vote.String(), VoteRaw: v.RawText,
		}); err != nil {
			s.fail("vote", d.ExternalID, err)
			return outcome
		}
		if v.Vote.Cast() {
			cast++
		}
	}

	// Pareo OVERRIDE pass (docs gap 4): an override IS a conditional update —
	// pareo members also carry an explicit `No Vota` row; first-insert-wins
	// semantics would lose every pareo (live-verified, votation 87461).
	for _, p := range d.Pareos {
		for _, dep := range []camara.DiputadoRef{p.A, p.B} {
			rid, err := s.resolveRep(ctx, q, dep)
			if err != nil {
				s.fail("pareo rep", d.ExternalID, err)
				return outcome
			}
			if err := q.UpsertPareoVote(ctx, database.UpsertPareoVoteParams{
				VotationID: vID, RepresentativeID: rid, VoteRaw: "Pareo",
			}); err != nil {
				s.fail("pareo", d.ExternalID, err)
				return outcome
			}
		}
	}

	// Audit totals cross-check: the four mapped-value equations (absent and
	// pareo rows never appear in API totals). Mismatch = warn counter, never
	// a failure — powering through new API values must not break on them.
	if cast > 0 {
		counts, err := q.GetVoteCounts(ctx, vID)
		if err == nil {
			c := votesOf(counts)
			if c[camara.VoteYes] != int64(d.Totals.Yes) || c[camara.VoteNo] != int64(d.Totals.No) ||
				c[camara.VoteAbstain] != int64(d.Totals.Abstain) || c[camara.VoteDispensed] != int64(d.Totals.Dispensed) {
				s.log.Warn("totals mismatch", "id", d.ExternalID,
					"src", d.Totals, "decoded", c, "cast", cast)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		s.fail("commit", d.ExternalID, err)
		return outcome
	}
	return Outcome{Hit: true, VotationID: vID}
}

// votesOf keys the per-value audit counts by the vote vocabulary.
func votesOf(counts []database.GetVoteCountsRow) map[camara.Vote]int64 {
	m := map[camara.Vote]int64{}
	for _, c := range counts {
		m[camara.Vote(c.Vote)] = c.N
	}
	return m
}

// --- Bill title invariant (docs/DESIGN.md gap 2) ------------------------------

const (
	titleSourceIngest   = "votation_articulo"
	titleSourceManual   = "manual"
	titleSourceEnriched = "enriched"
)

// billNameplate is the stand-in title for a bill whose votations so far carry
// no Articulo; it only names the row until a real title arrives.
func billNameplate(boletin string) string { return "Boletín " + boletin }

// BillTitle is the title block one votation proposes for its bill.
type billTitle struct {
	title     string         // Articulo, or the nameplate when the subject is empty
	titleDate sql.NullString // the proposal's date
	nameplate bool           // no Articulo: the title is the stand-in
}

// applyBillTitle runs inside the ingest tx (the ONLY place the title
// upgrade policy is decided): it re-reads the existing bill row and writes
// the proposal only when the recency rule wins.
func applyBillTitle(ctx context.Context, q *database.Queries, boletin string, in billTitle) error {
	existing, err := q.GetBill(ctx, boletin)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	// Row keeps its values on every not-wins branch; UpsertBillStub carries
	// no policy — the caller's slots are the chosen ones.
	if err == nil && !billTitleWins(existing, in) {
		return nil
	}
	return q.UpsertBillStub(ctx, database.UpsertBillStubParams{
		ID: boletin, Title: in.title,
		TitleSource: titleSourceIngest, TitleDate: in.titleDate,
	})
}

// billTitleWins is the recency rule, pure and order-independent (docs
// DESIGN.md gap 2: "the non-empty Articulo of the MOST RECENT votation;
// deterministic across ingest orders"):
//   - manual/enriched provenance is protected: never overwritten;
//   - a nameplate never replaces a recorded title;
//   - conversely, any Articulo beats a nameplate-only row (the nameplate was
//     never the "latest non-empty Articulo", there has been none yet);
//   - otherwise newer title_date wins; exact tie = greater title wins
//     (max(date,title) is order-independent);
//   - a dateless proposal never replaces a dated title.
func billTitleWins(existing database.Bill, in billTitle) bool {
	switch existing.TitleSource {
	case titleSourceManual, titleSourceEnriched:
		return false
	}
	if in.nameplate {
		return false
	}
	if existing.Title == billNameplate(existing.ID) {
		return true
	}
	if !existing.TitleDate.Valid {
		return true
	}
	if !in.titleDate.Valid {
		return false
	}
	switch {
	case in.titleDate.String > existing.TitleDate.String:
		return true
	case in.titleDate.String == existing.TitleDate.String:
		return in.title > existing.Title
	default:
		return false
	}
}

func (s *Syncer) resolveRep(ctx context.Context, q *database.Queries, d camara.DiputadoRef) (int64, error) {
	return q.UpsertRepresentativeFull(ctx, database.UpsertRepresentativeFullParams{
		ChamberID: "camara", ExternalID: strconv.Itoa(d.DIPID),
		FirstName: d.Nombre, LastName: d.ApellidoPaterno,
		SecondLastName: d.ApellidoMaterno, Gender: sql.NullString{}, BirthDate: sql.NullString{},
	})
}

func (s *Syncer) fail(op string, id int, err error) {
	s.log.Error("unit failed", "op", op, "id", id, "err", err)
}

// SyncDeputies refreshes the deputy roster:
//   - getDiputados_Vigentes membership is the ONLY source of active
//     (the API has no per-record active marker): set everyone inactive,
//     then grant active to the vigentes set
//   - the deactivate-all + per-deputy activate pass is ONE transaction
//     (bounded: one UPDATE + two statements per vigente): a mid-pass failure
//     rolls the roster back instead of leaving every remaining deputy
//     inactive — pages rendering mass "Histórico" is the failure this kills
//   - the all-history enrichment pass upserts only (never the active flag)
//     and stays outside the transaction to keep it short
func (s *Syncer) SyncDeputies(ctx context.Context) (int, error) {
	vig, err := s.client.DiputadosVigentes(ctx)
	if err != nil {
		return 0, fmt.Errorf("vigentes: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin roster pass: %w", err)
	}
	defer tx.Rollback()
	if err := applyVigentes(ctx, s.rosterQueries(tx), vig); err != nil {
		return 0, fmt.Errorf("apply vigentes: %w", err) // rollback keeps the previous roster
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit roster pass: %w", err)
	}

	all, err := s.client.Diputados(ctx)
	if err != nil {
		return 0, fmt.Errorf("all deputies: %w", err)
	}
	for _, d := range all {
		gender := nullIfEmpty(strings.TrimSpace(d.Sexo.Value))
		birth := nullIfEmpty(birthOnly(d.FechaNacimiento))
		if !gender.Valid && !birth.Valid {
			continue
		}
		if _, err := s.q.UpsertRepresentativeFull(ctx, database.UpsertRepresentativeFullParams{
			ChamberID: "camara", ExternalID: strconv.Itoa(d.DIPID),
			FirstName: d.Nombre, LastName: d.ApellidoPaterno,
			SecondLastName: d.ApellidoMaterno, Gender: gender, BirthDate: birth,
		}); err != nil {
			return 0, err
		}
	}
	return len(all), nil
}

// applyVigentes is the ROSTER TRANSACTION's body, written on the tx-scoped
// handle the caller passes. Errors roll the whole pass — the deactivate and
// every grant — back to the pre-pass roster.
func applyVigentes(ctx context.Context, q *database.Queries, vig []camara.DiputadoXML) error {
	if err := q.SetRepresentativesInactive(ctx, "camara"); err != nil {
		return fmt.Errorf("deactivate: %w", err)
	}
	for _, d := range vig {
		if _, err := q.UpsertRepresentativeFull(ctx, database.UpsertRepresentativeFullParams{
			ChamberID: "camara", ExternalID: strconv.Itoa(d.DIPID),
			FirstName: d.Nombre, LastName: d.ApellidoPaterno, SecondLastName: d.ApellidoMaterno,
			Gender:    nullIfEmpty(strings.TrimSpace(d.Sexo.Value)),
			BirthDate: nullIfEmpty(birthOnly(d.FechaNacimiento)),
		}); err != nil {
			return fmt.Errorf("upsert %d: %w", d.DIPID, err)
		}
		if err := q.SetRepresentativeActive(ctx, database.SetRepresentativeActiveParams{
			ChamberID: "camara", ExternalID: strconv.Itoa(d.DIPID),
		}); err != nil {
			return fmt.Errorf("activate %d: %w", d.DIPID, err)
		}
	}
	return nil
}
