package handlers

import (
	"errors"
	"net/http"

	"congress-visualizer/internal/database"
	"congress-visualizer/internal/render"
)

// PartiesData is the party registry page. The registry is a small curated
// set (data/parties.json), so this list is intentionally not paginated.
type PartiesData struct {
	Parties []database.Party
}

// Parties lists every curated party.
func (s *Server) Parties(w http.ResponseWriter, req *http.Request) {
	parties, err := s.Q.ListParties(req.Context())
	if err != nil {
		s.dbErr(w, req, err)
		return
	}
	s.page(w, render.PageParties, PartiesData{Parties: parties})
}

// PartyData is the party detail page.
type PartyData struct {
	Party    database.Party
	Members  []database.PartyMembersRow
	Cohesion PartyCohesionData
}

// PartyDetail shows one party and its current members, with the Phase-2
// Rice-cohesion summary (all history unless ?from/?to given — the window
// value is shared with the chart img on the page).
func (s *Server) PartyDetail(w http.ResponseWriter, req *http.Request) {
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
	members, err := s.Q.PartyMembers(req.Context(), id)
	if err != nil {
		s.dbErr(w, req, err)
		return
	}
	coh, err := s.PartyCohesion(req, id)
	if err != nil {
		// A malformed window is a 400 uniform across pages, not the 500
		// path of a query failure.
		var we windowError
		if errors.As(err, &we) {
			s.windowErr(w, req, err)
			return
		}
		s.dbErr(w, req, err)
		return
	}
	s.page(w, render.PageParty, PartyData{Party: party, Members: members, Cohesion: coh})
}
