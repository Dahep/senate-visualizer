package ingest

import (
	"context"
	"fmt"

	"congress-visualizer/internal/database"
)

// Sweep semantics (docs/DESIGN.md gap 1; interrogate fixes #4/#5): the
// votation ID space is sparse and NON-MONOTONIC (live-verified 2026-09-15:
// 42338 is a 2024-11-14 votation with nils on both sides; 1 and 40000 are
// misses), so sweeps never assume contiguity and termination is explicit —
// "reached the floor" and "stopped in a gap" are distinguishable outcomes.
//
// A sweep walks IDs one detail request at a time (discovery IS the detail
// fetch — zero extra requests) counting a MISS RUN. When the run reaches
// gapTolerance it probes geometrically ahead (run×10, ×100, ×1000):
//
//	hit  -> record the gap run and continue from the probe point
//	miss -> the probed windows are empty: this is the end of history
//
// Cursors advance ONLY from the sequential scanner; Drained queue candidates
// never move them (a queue candidate may sit far above an unswept frontier).

// ScanDirection selects sweep semantics; Down is the full load, Tick the
// incremental driver.
type ScanDirection int

const (
	// ScanDirectionDown is the full sweep: ceiling -> 1.
	ScanDirectionDown ScanDirection = iota
)

// Scan runs the full historical sweep (acquires the lease itself).
func (s *Syncer) Scan(ctx context.Context, dir ScanDirection) (ScanResult, error) {
	if ok, err := s.AcquireSyncLock(ctx, "cmd/sync"); err != nil {
		return ScanResult{}, err
	} else if !ok {
		return ScanResult{}, fmt.Errorf("another sweep holds %s", keySyncLock)
	}
	defer s.ReleaseSyncLock(ctx, "cmd/sync")
	return s.scanLocked(ctx)
}

// scanLocked runs the sweep assuming the lease is already held (Tick's
// cold-start fallthrough shares the lease instead of deadlocking).
func (s *Syncer) scanLocked(ctx context.Context) (ScanResult, error) {
	high, err := s.cursorInt(ctx, keyHighWater)
	if err != nil {
		return ScanResult{}, err
	}
	if high == 0 {
		if high, err = s.probeUp(ctx, seedID); err != nil {
			return ScanResult{}, fmt.Errorf("probe up from seed %d: %w", seedID, err)
		}
		if high == 0 {
			return ScanResult{}, fmt.Errorf("no hits above seed %d", seedID)
		}
	}
	resume, err := s.cursorInt(ctx, keyScanCursor)
	if err != nil {
		return ScanResult{}, err
	}
	if resume == 0 {
		resume = high
	}
	return s.sweepDown(ctx, resume)
}

// probeUp from a known-hit seed: every hit advances high_water; a widening
// miss run (T..×10T..capped) declares the ceiling. Returns the ceiling —
// the last hit observed.
func (s *Syncer) probeUp(ctx context.Context, seed int64) (int64, error) {
	var misses int64
	for id := seed; ; id++ {
		o := s.SyncVotationID(ctx, id)
		if o.Hit {
			misses = 0
			if err := s.setCursorInt(ctx, keyHighWater, id); err != nil {
				return 0, err
			}
			continue
		}
		misses++
		if misses >= gapTolerance*10 {
			return id - misses, nil
		}
	}
}

// sweepDown consumes IDs top-down; gap runs are probed geometrically ahead.
func (s *Syncer) sweepDown(ctx context.Context, from int64) (ScanResult, error) {
	var res ScanResult
	var missRun int64
	for id := from; id >= 1; id-- {
		o := s.SyncVotationID(ctx, id)
		if err := s.setCursorInt(ctx, keyScanCursor, id); err != nil {
			return res, err
		}
		if o.Hit {
			res.Hits++
			missRun = 0
			continue
		}
		res.Misses++
		missRun++
		if missRun < gapTolerance {
			continue
		}
		next, hit := s.probeAheadDown(ctx, id)
		if !hit {
			res.EndReason = ReasonFloor
			return res, s.afterSweep(&res)
		}
		res.GapRuns = append(res.GapRuns, int(missRun))
		s.log.Info("gap run crossed", "from", id, "next", next, "misses", missRun)
		missRun = 0
		id = next + 1 // the loop decrement lands on the probed hit
	}
	res.EndReason = ReasonFloor
	return res, s.afterSweep(&res)
}

// probeAheadDown searches below a tolerance miss run. The space is sparse
// with dense clusters (e.g. 42200..42339, then nils), so a coarse ladder
// alone is insufficient: first the geometric ladder (10x..1000x run), then a
// fine grid at run-width steps across the same span (≤1000 requests), so a
// cluster spanning as little as one gap-tolerance width cannot be missed.
// A hit anywhere resumes the sweep there.
func (s *Syncer) probeAheadDown(ctx context.Context, id int64) (int64, bool) {
	run := int64(gapTolerance)
	// ladder: distance run*10^k, k=1..4
	for k := int64(1); k <= 4; k++ {
		dist := run * pow10(k)
		if dist >= id {
			continue // out of range distance: probe the next ladder rung
		}
		if o := s.SyncVotationID(ctx, id-dist); o.Hit {
			return id - dist, true
		}
	}
	// fine grid: distance run*m for m=1..1000 (covers span run*1000)
	for m := int64(1); m <= 1000; m++ {
		probe := id - run*m
		if probe < 1 {
			break // below ID 1: no further runs reachable
		}
		if o := s.SyncVotationID(ctx, probe); o.Hit {
			return probe, true
		}
	}
	return 0, false
}

func pow10(k int64) int64 {
	v := int64(1)
	for i := int64(0); i < k; i++ {
		v *= 10
	}
	return v
}

// afterSweep records the operator-visible results: the depth criterion
// (MinVoteDate — a "complete" sweep spanning only recent days is loud).
func (s *Syncer) afterSweep(res *ScanResult) error {
	minDate, err := s.q.GetMinVoteDate(context.Background())
	if err != nil {
		return err
	}
	res.MinVoteDate = minDate
	return nil
}

// Tick is the incremental driver: overlap re-scan (self-heals late
// corrections), upward walk, queue drain, deputy refresh.
func (s *Syncer) Tick(ctx context.Context) (ScanResult, error) {
	const owner = "scheduler"
	if ok, err := s.AcquireSyncLock(ctx, owner); err != nil {
		return ScanResult{}, err
	} else if !ok {
		return ScanResult{}, fmt.Errorf("another sweep holds %s", keySyncLock)
	}
	defer s.ReleaseSyncLock(ctx, owner)

	var res ScanResult
	high, err := s.cursorInt(ctx, keyHighWater)
	if err != nil {
		return res, err
	}
	if high == 0 {
		return s.scanLocked(ctx) // cold start shares the already-held lease
	}

	// Upward walk with a small overlap below the frontier.
	const overlap = int64(200)
	start := high - overlap
	if start < 1 {
		start = 1
	}
	var missRun int64
	for id := start; ; id++ {
		o := s.SyncVotationID(ctx, id)
		if o.Hit {
			res.Hits++
			missRun = 0
			if err := s.setCursorInt(ctx, keyHighWater, id); err != nil {
				return res, err
			}
			continue
		}
		res.Misses++
		missRun++
		if missRun >= gapTolerance {
			break
		}
	}

	if err := s.Drain(ctx, drainBatch); err != nil {
		return res, err
	}
	if _, err := s.SyncDeputies(ctx); err != nil {
		return res, fmt.Errorf("deputies: %w", err)
	}
	if err := s.afterSweep(&res); err != nil {
		return res, err
	}
	res.EndReason = ReasonCtxDone
	return res, nil
}

// Drain consumes out-of-band candidates (boletín completeness checks, manual
// adds, future Senate sources) through the same unit of work with the
// documented lifecycle: miss stamps fetched_at; errors bump attempts and ≥
// maxAttempts quarantines (excluded from PendingCandidates, surfaced here).
func (s *Syncer) Drain(ctx context.Context, limit int) error {
	cands, err := s.q.PendingCandidates(ctx, int64(limit))
	if err != nil {
		return err
	}
	for _, c := range cands {
		var id int64
		fmt.Sscan(c.ExternalID, &id)
		if id == 0 {
			continue
		}
		o := s.SyncVotationID(ctx, id)
		if o.Miss {
			if err := s.q.MarkCandidateFetched(ctx); err != nil {
				return err
			}
		}
	}
	return nil
}

// EnqueueBoletinCheck feeds the completeness check: for one bill, harvest
// votation IDs from getVotaciones_Boletin (an out-of-band source) as
// candidates for the drain loop.
func (s *Syncer) EnqueueBoletinCheck(ctx context.Context, boletin string) (int, error) {
	vs, err := s.client.BoletinVotaciones(ctx, boletin)
	if err != nil {
		return 0, err
	}
	for _, v := range vs {
		if err := s.q.EnqueueCandidate(ctx, database.EnqueueCandidateParams{
			ChamberID: "camara", ExternalID: fmt.Sprint(v.ID), Source: "boletin_check",
		}); err != nil {
			return 0, err
		}
	}
	return len(vs), nil
}

const drainBatch = 50
