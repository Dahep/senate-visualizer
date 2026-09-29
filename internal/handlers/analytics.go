package handlers

import (
	"context"
	"net/http"
	"sort"
	"strconv"

	"congress-visualizer/internal/analytics"
	"congress-visualizer/internal/charts"
	"congress-visualizer/internal/database"
	"congress-visualizer/internal/render"
)

// Phase-2 analytics handlers (cohesion, loyalty/rebels, alignment). All are
// read-only; the arithmetic lives in internal/analytics (DESIGN.md gap 8).

// chamberDeputies is the chamber_id of the Cámara de Diputados in every
// analytics query (Senate views pending the Phase-2 Senate spike).
const chamberDeputies = "camara"

// currentLegisStart keeps pairwise/loyalty windows bounded and useful by
// default (2022-2026 legislature per docs/AFFILIATIONS_PLAN.md boundaries).
const currentLegisStart = "2022-03-11"

// minRebelsVotes / minPairJoint filter noise: one dissent in 2 votations is
// not data.
const (
	minRebelsVotes = 10
	minPairJoint   = 10
)

// voteWindow is the [From, To] vote-date range parsed from ?from/?to query
// params (ISO dates; empty string = open bound). This is the single
// parse/coerce point so a hand-typed value cannot poison the lexicographic
// date comparison in any analytics query.
type voteWindow struct {
	From, To string
}

func voteWindowFrom(req *http.Request) voteWindow {
	q := req.URL.Query()
	return voteWindow{
		From: coerceDate(q.Get("from")),
		To:   coerceDate(q.Get("to")),
	}
}

// defaultLegislature fills an open lower bound with the current legislature
// start — the rebels/alignment page policy (party cohesion defaults to all
// history instead).
func (w voteWindow) defaultLegislature() voteWindow {
	if w.From == "" {
		w.From = currentLegisStart
	}
	return w
}

// coerceDate rejects non-ISO-date input (8 digits, '-' at positions 4 and
// 7) so a hand-typed window cannot poison a lexicographic date comparison.
func coerceDate(s string) string {
	if len(s) != 10 || s[4] != '-' || s[7] != '-' {
		return ""
	}
	for i := 0; i < 10; i++ {
		if i == 4 || i == 7 {
			if s[i] != '-' {
				return ""
			}
			continue
		}
		if s[i] < '0' || s[i] > '9' {
			return ""
		}
	}
	return s
}

// --- Party cohesion ----------------------------------------------------------

// PartyCohesionChart serves the monthly Rice-index trend of one party as a
// standalone SVG (image/svg+xml). Default window: all ingested history.
func (s *Server) PartyCohesionChart(w http.ResponseWriter, req *http.Request) {
	id, ok := idParam(req)
	if !ok {
		http.NotFound(w, req)
		return
	}
	party, err := s.Q.GetParty(req.Context(), id)
	if err != nil {
		s.dbErr(w, req, err)
		return
	}
	wv := voteWindowFrom(req)
	rows, err := s.Q.GetCohesionRows(req.Context(), database.GetCohesionRowsParams{
		Chamber: chamberDeputies, FromDate: wv.From, ToDate: wv.To, PartyID: id,
	})
	if err != nil {
		s.dbErr(w, req, err)
		return
	}
	series := analytics.CohesionSeries(toCohesionRows(rows))
	svg, err := charts.TrendLine(series, party.Color, 900, 320)
	if err != nil {
		s.dbErr(w, req, err)
		return
	}
	s.writeSVG(w, svg)
}

// PartyCohesionData is the summary block on the party detail page.
type PartyCohesionData struct {
	analytics.Aggregate
	From, To string
}

// PartyCohesion computes the party-summary numbers for the party page.
func (s *Server) PartyCohesion(req *http.Request, partyID int64) (PartyCohesionData, error) {
	wv := voteWindowFrom(req)
	rows, err := s.Q.GetCohesionRows(req.Context(), database.GetCohesionRowsParams{
		Chamber: chamberDeputies, FromDate: wv.From, ToDate: wv.To, PartyID: partyID,
	})
	if err != nil {
		return PartyCohesionData{}, err
	}
	agg := analytics.Cohesion(toCohesionRows(rows))
	return PartyCohesionData{Aggregate: agg, From: wv.From, To: wv.To}, nil
}

// --- Loyalty / rebels ---------------------------------------------------------

// RebelsData is the /rebels page: representatives ordered by party loyalty
// ascending (the dissidents first), default window = current legislature.
type RebelsData struct {
	From         string
	To           string
	MinVotations int64
	Rows         []analytics.RepLoyalty
}

// Rebels renders the loyalty-ascending ranking over the window.
func (s *Server) Rebels(w http.ResponseWriter, req *http.Request) {
	wv := voteWindowFrom(req).defaultLegislature()
	all, err := s.rebelsRanking(req.Context(), wv)
	if err != nil {
		s.dbErr(w, req, err)
		return
	}
	shown := make([]analytics.RepLoyalty, 0, len(all))
	for _, l := range all {
		if l.Votations >= minRebelsVotes {
			shown = append(shown, l)
		}
	}
	s.page(w, render.PageRebels, RebelsData{
		From:         wv.From,
		To:           wv.To,
		MinVotations: minRebelsVotes,
		Rows:         shown,
	})
}

// rebelsRanking fetches (and caches) the rep-level loyalty ranking for a
// window. The underlying query streams every cast vote in the window
// (~500k rows for 2022+), so repeat views reuse the computed ranking; the
// 10-minute TTL lets freshly ingested votes appear without a restart.
func (s *Server) rebelsRanking(ctx context.Context, wv voteWindow) ([]analytics.RepLoyalty, error) {
	key := wv.From + "|" + wv.To
	if v, ok := s.rankCache.get(key); ok {
		return v, nil
	}
	rows, err := s.Q.GetLoyaltyRows(ctx, database.GetLoyaltyRowsParams{
		Chamber: chamberDeputies, FromDate: wv.From, ToDate: wv.To,
	})
	if err != nil {
		return nil, err
	}
	all := analytics.Loyalty(toLoyaltyRows(rows))
	s.rankCache.set(key, all)
	return all, nil
}

// --- Alignment matrix ----------------------------------------------------------

// AlignmentData is the /alignment page: pairwise same-cast-vote agreement.
// Party filter (optional) applies party-at-date to both pair members; empty
// window defaults to the current legislature. The computed pair set is huge
// (~12k pairs for the full chamber), so the page renders only the top
// maxPairsShown of the requested order (most/least aligned).
type AlignmentData struct {
	From       string
	To         string
	PartyID    int64
	Parties    []database.Party
	MinJoint   int64
	Order      string // 'most' | 'least'
	Capped     bool
	TotalPairs int
	Pairs      []PairRow
}

// PairRow is one deputy pair with its same-vote agreement rate.
type PairRow struct {
	AN, BN    string // display names
	Joint     int64
	Same      int64
	Agreement float64
}

// maxPairsShown bounds the rendered table (the query output is already
// grouped in SQL; this only trims the response size).
const maxPairsShown = 200

// Alignment renders the pairwise agreement list over the window; the heavy
// GROUP BY runs in SQL and the handler only formats rows.
func (s *Server) Alignment(w http.ResponseWriter, req *http.Request) {
	wv := voteWindowFrom(req).defaultLegislature()
	q := req.URL.Query()
	partyID, _ := strconv.ParseInt(q.Get("party"), 10, 64)
	order := q.Get("order")
	if order != "least" {
		order = "most"
	}

	parties, err := s.Q.ListParties(req.Context())
	if err != nil {
		s.dbErr(w, req, err)
		return
	}
	pairs, err := s.pairAgreementRows(req.Context(), wv, partyID)
	if err != nil {
		s.dbErr(w, req, err)
		return
	}
	kept := make([]database.GetPairAgreementRowsRow, 0, len(pairs))
	for _, p := range pairs {
		if p.Joint >= minPairJoint {
			kept = append(kept, p)
		}
	}
	total := len(kept)
	asc := order == "least"
	sort.SliceStable(kept, func(i, j int) bool {
		ai := kept[i].Same.Float64 / float64(kept[i].Joint)
		aj := kept[j].Same.Float64 / float64(kept[j].Joint)
		if ai != aj {
			if asc {
				return ai < aj
			}
			return ai > aj
		}
		// Joint tiebreak keeps the same direction in both orders so the
		// 'least' view stays a strict mirror of the 'most' view.
		return kept[i].Joint > kept[j].Joint
	})
	capped := total > maxPairsShown
	if capped {
		kept = kept[:maxPairsShown]
	}
	names, err := s.representativeNames(req.Context(), pairIDs(kept))
	if err != nil {
		s.dbErr(w, req, err)
		return
	}
	out := make([]PairRow, len(kept))
	for i, p := range kept {
		out[i] = PairRow{
			AN:        names[p.AID],
			BN:        names[p.BID],
			Joint:     p.Joint,
			Same:      int64(p.Same.Float64),
			Agreement: p.Same.Float64 / float64(p.Joint),
		}
	}
	s.page(w, render.PageAlignment, AlignmentData{
		From: wv.From, To: wv.To,
		PartyID: partyID, Parties: parties, Order: order,
		MinJoint: minPairJoint, Pairs: out,
		Capped: capped, TotalPairs: total,
	})
}

// pairAgreementRows fetches (and caches) the alignment self-join. The cache
// key is the exact parameter set; the whole-chamber query costs ~5s of SQLite
// work, and review/repeat views should not pay it again within the TTL.
func (s *Server) pairAgreementRows(ctx context.Context, wv voteWindow, partyID int64) ([]database.GetPairAgreementRowsRow, error) {
	key := wv.From + "|" + wv.To + "|" + strconv.FormatInt(partyID, 10)
	if v, ok := s.pairCache.get(key); ok {
		return v, nil
	}
	rows, err := s.Q.GetPairAgreementRows(ctx, database.GetPairAgreementRowsParams{
		Chamber: chamberDeputies, FromDate: wv.From, ToDate: wv.To, PartyID: partyID,
	})
	if err != nil {
		return nil, err
	}
	s.pairCache.set(key, rows)
	return rows, nil
}

// pairIDs collects the distinct rep IDs referenced by the shown pairs.
func pairIDs(pairs []database.GetPairAgreementRowsRow) []int64 {
	seen := map[int64]struct{}{}
	ids := make([]int64, 0, 2*len(pairs))
	for _, p := range pairs {
		for _, id := range [2]int64{p.AID, p.BID} {
			if _, ok := seen[id]; !ok {
				seen[id] = struct{}{}
				ids = append(ids, id)
			}
		}
	}
	return ids
}

// representativeNames returns rep id -> "Apellidos, Nombre" for the given
// IDs only.
func (s *Server) representativeNames(ctx context.Context, ids []int64) (map[int64]string, error) {
	rows, err := s.Q.GetRepresentativeNames(ctx, ids)
	if err != nil {
		return nil, err
	}
	m := make(map[int64]string, len(rows))
	for _, r := range rows {
		m[r.ID] = analytics.FullName(r.FirstName, r.LastName, r.SecondLastName)
	}
	return m, nil
}

// --- Conversions -------------------------------------------------------------

func toCohesionRows(rows []database.GetCohesionRowsRow) []analytics.CohesionRow {
	out := make([]analytics.CohesionRow, len(rows))
	for i, r := range rows {
		out[i] = analytics.CohesionRow{
			PartyID:    r.PartyID,
			VotationID: r.VotationID,
			VoteDate:   r.VoteDate,
			Vote:       r.Vote,
			N:          r.N,
		}
	}
	return out
}

func toLoyaltyRows(rows []database.GetLoyaltyRowsRow) []analytics.LoyaltyRow {
	out := make([]analytics.LoyaltyRow, len(rows))
	for i, r := range rows {
		out[i] = analytics.LoyaltyRow{
			RepID:          r.RepID,
			FirstName:      r.FirstName,
			LastName:       r.LastName,
			SecondLastName: r.SecondLastName,
			PartyID:        r.PartyID,
			PartyShort:     r.PartyShort,
			VotationID:     r.VotationID,
			Vote:           r.Vote,
		}
	}
	return out
}

// --- Representative loyalty -----------------------------------------------------

// RepresentativeLoyaltyData is the loyalty summary for the rep profile page.
type RepresentativeLoyaltyData struct {
	Score     float64 // fraction of cast votes matching the party's modal vote
	Votations int64
	Majority  int64
}

// RepresentativeLoyalty computes the rep-profile loyalty summary: the
// rep's entry in the full-chamber loyalty ranking (open window = full
// history). The party modal must come from ALL party members, so the
// rep-filtered rows cannot answer it; reusing the shared cached ranking
// keeps rep pages cheap after the first one.
func (s *Server) RepresentativeLoyalty(req *http.Request, repID int64) (RepresentativeLoyaltyData, error) {
	loyalty, err := s.rebelsRanking(req.Context(), voteWindow{})
	if err != nil {
		return RepresentativeLoyaltyData{}, err
	}
	for _, l := range loyalty {
		if l.RepID == repID {
			return RepresentativeLoyaltyData{
				Score:     l.Score,
				Votations: l.Votations,
				Majority:  l.Majority,
			}, nil
		}
	}
	return RepresentativeLoyaltyData{}, nil
}
