package handlers

import (
	"net/http"

	"congress-visualizer/internal/database"
	"congress-visualizer/internal/render"
)

// RepresentativesData is the representative list page.
type RepresentativesData struct {
	Representatives []database.ListRepresentativesRow
	Pagination      Pagination
}

// Representatives lists deputies (active first), paginated at 25 per page.
func (s *Server) Representatives(w http.ResponseWriter, req *http.Request) {
	page := pageParam(req)
	pg := newPagination(page, 0)
	rows, err := s.Q.ListRepresentatives(req.Context(), database.ListRepresentativesParams{
		Column1: "camara", ChamberID: "camara",
		Limit: pageSize, Offset: pg.offset(),
	})
	if err != nil {
		s.dbErr(w, req, err)
		return
	}
	total, err := s.Q.CountRepresentatives(req.Context(), database.CountRepresentativesParams{
		Column1: "camara", ChamberID: "camara",
	})
	if err != nil {
		s.dbErr(w, req, err)
		return
	}
	s.page(w, render.PageRepresentatives, RepresentativesData{
		Representatives: rows, Pagination: newPagination(page, total),
	})
}

// historySize is the representative voting-history page size.
const historySize = 50

// RepresentativeData is the representative profile page.
type RepresentativeData struct {
	Representative database.GetRepresentativeRow
	History        []database.GetRepresentativeVotingHistoryRow
	Pagination     Pagination
}

// RepresentativeDetail shows a deputy and their voting history (50/page).
func (s *Server) RepresentativeDetail(w http.ResponseWriter, req *http.Request) {
	id, ok := idParam(req)
	if !ok {
		http.NotFound(w, req)
		return
	}
	rep, err := s.Q.GetRepresentative(req.Context(), id)
	if err != nil {
		s.dbErr(w, req, err)
		return
	}
	page := pageParam(req)
	history, err := s.Q.GetRepresentativeVotingHistory(req.Context(), database.GetRepresentativeVotingHistoryParams{
		RepresentativeID: id,
		Limit:            historySize,
		Offset:           (page - 1) * historySize,
	})
	if err != nil {
		s.dbErr(w, req, err)
		return
	}
	total, err := s.Q.CountRepresentativeVotes(req.Context(), id)
	if err != nil {
		s.dbErr(w, req, err)
		return
	}
	s.page(w, render.PageRepresentative, RepresentativeData{
		Representative: rep,
		History:        history,
		Pagination:     Pagination{Page: page, Size: historySize, Total: total},
	})
}
