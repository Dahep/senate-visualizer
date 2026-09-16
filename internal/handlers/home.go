package handlers

import (
	"database/sql"

	"net/http"

	"congress-visualizer/internal/database"
	"congress-visualizer/internal/render"
)

// HomeData is the landing page: headline counts plus the latest votations.
type HomeData struct {
	Votations       int64
	Representatives int64
	Parties         int64
	Bills           int64
	Latest          []database.ListVotationsRow
}

// Home renders the landing page.
func (s *Server) Home(w http.ResponseWriter, req *http.Request) {
	ctx := req.Context()
	data := HomeData{}

	var err error
	if data.Votations, err = s.Q.CountVotations(ctx, database.CountVotationsParams{
		Column1: "camara", ChamberID: "camara", Column3: "", Result: sql.NullString{}, Column5: "", BillID: sql.NullString{},
	}); err != nil {
		s.dbErr(w, req, err)
		return
	}
	if data.Representatives, err = s.Q.CountRepresentatives(ctx, database.CountRepresentativesParams{
		Column1: "camara", ChamberID: "camara",
	}); err != nil {
		s.dbErr(w, req, err)
		return
	}
	if data.Bills, err = s.Q.CountBills(ctx, "%"); err != nil {
		s.dbErr(w, req, err)
		return
	}
	parties, err := s.Q.ListParties(ctx)
	if err != nil {
		s.dbErr(w, req, err)
		return
	}
	data.Parties = int64(len(parties))

	latest, err := s.Q.ListVotations(ctx, database.ListVotationsParams{
		Column1: "camara", ChamberID: "camara", Column3: "", Result: sql.NullString{}, Column5: "", BillID: sql.NullString{}, Limit: 10,
	})
	if err != nil {
		s.dbErr(w, req, err)
		return
	}
	data.Latest = latest

	s.page(w, render.PageHome, data)
}
