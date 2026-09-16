package handlers

import (
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
	Party   database.Party
	Members []database.PartyMembersRow
}

// PartyDetail shows one party and its current members.
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
	s.page(w, render.PageParty, PartyData{Party: party, Members: members})
}
