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
	keyDeputiesAt = "camara.deputies.synced_at"
)

const (
	gapTolerance = 50    // consecutive misses ending a sweep leg before probe-ahead widens
	maxAttempts  = 5     // votation_candidates quarantine threshold (docs: candidates.sql)
	seedID       = 87460 // live-verified current-window ID; sweeps grow from here
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
	ReasonFloor   ScanReason = "floor"
	ReasonTolerance ScanReason = "tolerance"
	ReasonCtxDone ScanReason = "context"
)

// ScanResult is the sweep outcome — including the depth check:
// MinVoteDate lets the operator see at a glance whether the scan actually
// reached history rather than stopping a week back.
type ScanResult struct {
	EndReason   ScanReason
	Hits        int
	Misses      int
	GapRuns     []int // gap runs the sweep crossed (widths)
	Pending     int // out-of-band candidates still pending (incl. quarantined)
	MinVoteDate string
}

func (r ScanResult) Log(l *slog.Logger, dir string) {
	l.Info("scan done", "dir", dir, "end_reason", r.EndReason,
		"hits", r.Hits, "misses", r.Misses, "min_vote_date", r.MinVoteDate)
}

// Syncer is the one write path into ingested tables.
type Syncer struct {
	db     *sql.DB // transaction boundary for the per-votation unit of work
	q      *database.Queries
	client *camara.Client
	log    *slog.Logger
}

func NewSyncer(db *sql.DB, q *database.Queries, c *camara.Client, l *slog.Logger) *Syncer {
	if l == nil {
		l = slog.Default()
	}
	return &Syncer{db: db, q: q, client: c, log: l}
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

// AcquireSyncLock claims a 2h lease; false = another sweep holds it.
func (s *Syncer) AcquireSyncLock(ctx context.Context, owner string) (bool, error) {
	value := fmt.Sprintf("%s|%d", owner, time.Now().Add(2*time.Hour).Unix())
	n, err := s.q.AcquireSyncLock(ctx, value)
	if n > 0 || err != nil {
		return n > 0, err
	}
	// The CAS buys it if we own the current lease (same-owner refresh).
	var cur string
	cur, err = s.q.GetSyncValue(ctx, keySyncLock)
	if errors.Is(err, sql.ErrNoRows) {
		cur = "" // never leased: the empty row means free
	} else if err != nil {
		return false, err
	}
	if ownerIsh(cur, owner) {
		n, err = s.q.AcquireSyncLock(ctx, value)
		return n > 0, err
	}
	return false, nil // someone else's lease
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
		if strings.TrimSpace(title) == "" {
			title = "Boletín " + d.Boletin
		}
		if err := q.UpsertBillStub(ctx, database.UpsertBillStubParams{
			ID: d.Boletin, Title: title,
			TitleSource: "votation_articulo", TitleDate: nullIfEmpty(d.VoteDate),
		}); err != nil {
			s.fail("bill", d.ExternalID, err)
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
		if v.Vote == camara.VoteYes || v.Vote == camara.VoteNo || v.Vote == camara.VoteAbstain {
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
			if c["yes"] != int64(d.Totals.Yes) || c["no"] != int64(d.Totals.No) ||
				c["abstain"] != int64(d.Totals.Abstain) || c["dispensed"] != int64(d.Totals.Dispensed) {
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

func votesOf(counts []database.GetVoteCountsRow) map[string]int64 {
	m := map[string]int64{}
	for _, c := range counts {
		m[c.Vote] = c.N
	}
	return m
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
//   - every vigentes DIPID is upserted, then the all-history list enriches
//     stubs (gender/birthdate; Fecha_Nacimiento can be absent e.g. DIPID 485)
func (s *Syncer) SyncDeputies(ctx context.Context) (int, error) {
	vig, err := s.client.DiputadosVigentes(ctx)
	if err != nil {
		return 0, fmt.Errorf("vigentes: %w", err)
	}
	if err := s.q.SetRepresentativesInactive(ctx, "camara"); err != nil {
		return 0, fmt.Errorf("deactivate: %w", err)
	}
	grantActive := func(d camara.DiputadoXML) error {
		if _, err := s.q.UpsertRepresentativeFull(ctx, database.UpsertRepresentativeFullParams{
			ChamberID: "camara", ExternalID: strconv.Itoa(d.DIPID),
			FirstName: d.Nombre, LastName: d.ApellidoPaterno, SecondLastName: d.ApellidoMaterno,
			Gender: nullIfEmpty(strings.TrimSpace(d.Sexo.Value)), BirthDate: nullIfEmpty(birthOnly(d.FechaNacimiento)),
		}); err != nil {
			return err
		}
		return s.q.SetRepresentativeActive(ctx, database.SetRepresentativeActiveParams{
			ChamberID: "camara", ExternalID: strconv.Itoa(d.DIPID),
		})
	}
	for _, d := range vig {
		if err := grantActive(d); err != nil {
			return 0, err
		}
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
	return len(all), s.q.UpsertSyncValue(ctx, database.UpsertSyncValueParams{Key: keyDeputiesAt, Value: "done"})
}
