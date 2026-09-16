package handlers

import (
	"net/http"

	"congress-visualizer/internal/database"
	"congress-visualizer/internal/render"
)

// VotationsData is the votation list page.
type VotationsData struct {
	Votations  []database.ListVotationsRow
	Pagination Pagination
}

// Votations lists votations, newest first, paginated at 25 per page.
func (s *Server) Votations(w http.ResponseWriter, req *http.Request) {
	page := pageParam(req)
	pg := newPagination(page, 0)
	filter := database.ListVotationsParams{
		Column1: "camara", ChamberID: "camara",
		Limit: pageSize, Offset: pg.offset(),
	}
	rows, err := s.Q.ListVotations(req.Context(), filter)
	if err != nil {
		s.dbErr(w, req, err)
		return
	}
	total, err := s.Q.CountVotations(req.Context(), database.CountVotationsParams{
		Column1: "camara", ChamberID: "camara",
	})
	if err != nil {
		s.dbErr(w, req, err)
		return
	}
	s.page(w, render.PageVotations, VotationsData{
		Votations: rows, Pagination: newPagination(page, total),
	})
}

// VoterRow is one deputy's vote inside a party group.
type VoterRow struct {
	RepID int64
	Name  string
	Vote  string
	Raw   string
}

// PartyTally is one party's per-value counts plus its deputy rows.
type PartyTally struct {
	Short     string
	Name      string
	Color     string
	Yes       int64
	No        int64
	Abstain   int64
	Absent    int64
	Dispensed int64
	Paired    int64
	Other     int64
	Voters    []VoterRow
}

// Cast is the number of position votes (yes+no+abstain) — the Rice base.
func (t PartyTally) Cast() int64 { return t.Yes + t.No + t.Abstain }

// VoteBreakdown is the grouped per-party view of one votation.
type VoteBreakdown struct {
	Parties []PartyTally
	Total   int64
}

// buildBreakdown groups vote rows (already ordered by party, last name from
// the query) preserving that order; counts derive from individual_votes rows
// only — the stored votation totals are audit copies, never displayed.
func buildBreakdown(rows []database.GetVotesByVotationRow) VoteBreakdown {
	idx := map[string]int{}
	b := VoteBreakdown{Parties: []PartyTally{}}
	for _, r := range rows {
		i, ok := idx[r.PartyName]
		if !ok {
			i = len(b.Parties)
			idx[r.PartyName] = i
			b.Parties = append(b.Parties, PartyTally{
				Short: r.PartyShort, Name: r.PartyName, Color: r.PartyColor,
				Voters: []VoterRow{},
			})
		}
		p := &b.Parties[i]
		switch r.Vote {
		case "yes":
			p.Yes++
		case "no":
			p.No++
		case "abstain":
			p.Abstain++
		case "absent":
			p.Absent++
		case "dispensed":
			p.Dispensed++
		case "paired":
			p.Paired++
		default:
			p.Other++
		}
		name := r.LastName
		if r.SecondLastName != "" {
			name += " " + r.SecondLastName
		}
		name += ", " + r.FirstName
		p.Voters = append(p.Voters, VoterRow{RepID: r.RepID, Name: name, Vote: r.Vote, Raw: r.VoteRaw})
		b.Total++
	}
	return b
}

// breakdownFor loads the vote rows of one votation and groups them.
func (s *Server) breakdownFor(req *http.Request, votationID int64) (VoteBreakdown, error) {
	votes, err := s.Q.GetVotesByVotation(req.Context(), votationID)
	if err != nil {
		return VoteBreakdown{}, err
	}
	return buildBreakdown(votes), nil
}

// VotationDetailData is the votation detail page.
type VotationDetailData struct {
	Votation        database.Votation
	BillTitle       string
	BillProvisional bool // title still sourced from a votation's Articulo
	Breakdown       VoteBreakdown
}

// VotationDetail shows one votation: metadata, bill link, chart, breakdown.
func (s *Server) VotationDetail(w http.ResponseWriter, req *http.Request) {
	id, ok := idParam(req)
	if !ok {
		http.NotFound(w, req)
		return
	}
	v, err := s.Q.GetVotation(req.Context(), id)
	if err != nil {
		s.dbErr(w, req, err)
		return
	}
	breakdown, err := s.breakdownFor(req, id)
	if err != nil {
		s.dbErr(w, req, err)
		return
	}
	data := VotationDetailData{Votation: v, Breakdown: breakdown}
	if v.BillID.Valid {
		if b, err := s.Q.GetBill(req.Context(), v.BillID.String); err == nil {
			data.BillTitle = b.Title
			data.BillProvisional = b.TitleSource == "votation_articulo"
		}
	}
	s.page(w, render.PageVotationDetail, data)
}

// VoteBreakdownPartial serves the HTMX swap target on its own.
func (s *Server) VoteBreakdownPartial(w http.ResponseWriter, req *http.Request) {
	id, ok := idParam(req)
	if !ok {
		http.NotFound(w, req)
		return
	}
	if _, err := s.Q.GetVotation(req.Context(), id); err != nil {
		s.dbErr(w, req, err)
		return
	}
	breakdown, err := s.breakdownFor(req, id)
	if err != nil {
		s.dbErr(w, req, err)
		return
	}
	if err := s.R.Partial(w, "vote_breakdown", breakdown); err != nil {
		s.Log.Error("render partial", "name", "vote_breakdown", "err", err)
	}
}
