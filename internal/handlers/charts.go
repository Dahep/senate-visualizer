package handlers

import (
	"net/http"
	"sort"

	"congress-visualizer/internal/charts"
	"congress-visualizer/internal/database"
)

// PartyBreakdownChart serves the per-party composition of one votation as a
// standalone SVG document (Content-Type image/svg+xml). Labels are escaped
// inside charts.PartyStack; the SVG is never inlined into HTML.
func (s *Server) PartyBreakdownChart(w http.ResponseWriter, req *http.Request) {
	id, ok := idParam(req)
	if !ok {
		http.NotFound(w, req)
		return
	}
	if _, err := s.Q.GetVotation(req.Context(), id); err != nil {
		s.dbErr(w, req, err)
		return
	}
	rows, err := s.Q.GetPartyVoteCounts(req.Context(), id)
	if err != nil {
		s.dbErr(w, req, err)
		return
	}
	svg, err := charts.PartyStack(partyLayers(rows), 900, 420)
	if err != nil {
		s.dbErr(w, req, err)
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	if _, err := w.Write(svg); err != nil {
		s.Log.Error("write chart", "err", err)
	}
}

// partyLayers groups the (party, vote, n) rows into one layer per party,
// ordered by cast votes descending for a stable, readable chart.
func partyLayers(rows []database.GetPartyVoteCountsRow) []charts.PartyLayer {
	idx := map[int64]int{}
	layers := []charts.PartyLayer{}
	for _, r := range rows {
		i, ok := idx[r.PartyID]
		if !ok {
			i = len(layers)
			idx[r.PartyID] = i
			layers = append(layers, charts.PartyLayer{
				Label: r.ShortName, Color: r.Color,
			})
		}
		switch r.Vote {
		case "yes":
			layers[i].Yes += r.N
		case "no":
			layers[i].No += r.N
		case "abstain":
			layers[i].Abstain += r.N
		}
	}
	sort.SliceStable(layers, func(i, j int) bool {
		ci := layers[i].Yes + layers[i].No + layers[i].Abstain
		cj := layers[j].Yes + layers[j].No + layers[j].Abstain
		if ci != cj {
			return ci > cj
		}
		return layers[i].Label < layers[j].Label
	})
	return layers
}
