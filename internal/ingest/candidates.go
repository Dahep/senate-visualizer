package ingest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"congress-visualizer/internal/database"
)

// maxAttempts is the votation_candidates quarantine threshold — the SINGLE
// source of truth: the pending queries receive it as sqlc.arg(max_attempts)
// (db/queries/candidates.sql), so the SQL can never drift from this constant.
const maxAttempts = 5

// votationSyncer is the unit-of-work seam the queue drains through.
// *Syncer satisfies it (SyncVotationID is THE unit of work — one detail
// request, one transaction); tests substitute a stub.
type votationSyncer interface {
	SyncVotationID(ctx context.Context, externalID int64) Outcome
}

var _ votationSyncer = (*Syncer)(nil)

// candidateQueue owns the votation_candidates lifecycle (db/migrations
// comment, docs/DESIGN.md): pending -> fetched on hit OR miss, stamped by the
// row's identity — never batch-wide; on transport/decode/upsert failure
// attempts++/last_error; attempts >= maxAttempts quarantines the row
// (excluded from the pending query, surfaced in the result). A non-numeric
// external_id is a data defect and counts as failure toward quarantine
// instead of silently dropping discovery data.
type candidateQueue struct {
	q   *database.Queries
	log *slog.Logger
}

// DrainResult reports one queue-drain pass.
type DrainResult struct {
	Drained     int                          // rows examined this pass
	Hits        int                          // votation persisted AND stamped fetched
	Misses      int                          // confirmed absent AND stamped fetched
	Failed      int                          // error outcomes: attempts++, last_error set
	Quarantined []database.VotationCandidate // rows that crossed maxAttempts this pass
	Pending     int                          // rows still pending after the pass
}

func (r DrainResult) Log(l *slog.Logger) {
	l.Info("queue drained",
		"drained", r.Drained, "hits", r.Hits, "misses", r.Misses,
		"failed", r.Failed, "quarantined", len(r.Quarantined), "pending", r.Pending)
}

// drain applies the lifecycle to every pending row: parse the external ID,
// run the unit of work, then stamp fetched (hit/miss) or record a failure.
// Misses and hits are stamped by (chamber_id, external_id) so one drained
// row can never quietly resolve its queue-mates. Errors and unparseable IDs
// retry until they cross the quarantine threshold. Drain NEVER advances
// high_water (a queue candidate may sit far above the unswept frontier).
func (cq candidateQueue) drain(ctx context.Context, vs votationSyncer, limit int) (DrainResult, error) {
	var res DrainResult
	cands, err := cq.q.PendingCandidates(ctx, database.PendingCandidatesParams{
		MaxAttempts: maxAttempts,
		RowLimit:    int64(limit),
	})
	if err != nil {
		return res, err
	}
	for _, c := range cands {
		res.Drained++
		var cause error // non-nil: record one failed attempt against the row
		id, err := parseExternalID(c)
		switch {
		case err != nil:
			res.Failed++
			cause = err
		default:
			o := vs.SyncVotationID(ctx, id)
			if !o.Hit && !o.Miss {
				res.Failed++
				cause = errors.New("detail fetch failed (transport, decode, or upsert error)")
				break
			}
			if o.Hit {
				res.Hits++
			} else {
				res.Misses++
			}
			if err := cq.q.MarkCandidateFetched(ctx, database.MarkCandidateFetchedParams{
				ChamberID: c.ChamberID, ExternalID: c.ExternalID,
			}); err != nil {
				return res, fmt.Errorf("mark fetched %s/%s: %w", c.ChamberID, c.ExternalID, err)
			}
		}
		if cause == nil {
			continue
		}
		quarantined, err := cq.fail(ctx, c, cause)
		if err != nil {
			return res, fmt.Errorf("fail candidate %s/%s: %w", c.ChamberID, c.ExternalID, err)
		}
		if quarantined {
			res.Quarantined = append(res.Quarantined, c)
			cq.log.Warn("candidate quarantined",
				"chamber_id", c.ChamberID, "external_id", c.ExternalID,
				"attempts", maxAttempts, "last_error", cause.Error())
		}
	}
	n, err := cq.q.CountPendingCandidates(ctx, int64(maxAttempts))
	if err != nil {
		return res, err
	}
	res.Pending = int(n)
	return res, nil
}

// fail records one failed attempt (attempts++, last_error) and reports
// whether the row just crossed the quarantine threshold.
func (cq candidateQueue) fail(ctx context.Context, c database.VotationCandidate, cause error) (bool, error) {
	attempts, err := cq.q.FailCandidate(ctx, database.FailCandidateParams{
		ChamberID: c.ChamberID, ExternalID: c.ExternalID,
		LastError: sql.NullString{String: cause.Error(), Valid: true},
	})
	if err != nil {
		return false, err
	}
	return attempts >= maxAttempts, nil
}

// parseExternalID enforces a positive integer votation ID. The previous
// fmt.Sscan silently parsed garbage to 0 (and every drain skipped the row,
// neither retrying nor quarantining); an unparseable discovered ID is a
// failure toward quarantine instead.
func parseExternalID(c database.VotationCandidate) (int64, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(c.ExternalID), 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("external_id %q is not a votation ID", c.ExternalID)
	}
	return id, nil
}
