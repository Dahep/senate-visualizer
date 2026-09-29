package handlers

import (
	"database/sql"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"congress-visualizer/internal/database"
	"congress-visualizer/internal/render"
)

// BillsData is the bill (boletín) list page, searchable by boletín number.
type BillsData struct {
	Bills      []database.Bill
	Query      string
	Pagination Pagination
}

// likePattern wraps a search term for the LIKE filter; "%" matches all.
func likePattern(q string) string {
	q = strings.TrimSpace(q)
	if q == "" {
		return "%"
	}
	return "%" + q + "%"
}

// Bills lists bills, most recently titled first, paginated at 25 per page.
func (s *Server) Bills(w http.ResponseWriter, req *http.Request) {
	page := pageParam(req)
	q := strings.TrimSpace(req.URL.Query().Get("q"))
	pg := newPagination(page, 0)
	rows, err := s.Q.ListBills(req.Context(), database.ListBillsParams{
		ID: likePattern(q), Limit: pageSize, Offset: pg.offset(),
	})
	if err != nil {
		s.dbErr(w, req, err)
		return
	}
	total, err := s.Q.CountBills(req.Context(), likePattern(q))
	if err != nil {
		s.dbErr(w, req, err)
		return
	}
	s.page(w, render.PageBills, BillsData{
		Bills: rows, Query: q, Pagination: newPagination(page, total),
	})
}

// BillData is the bill detail page.
type BillData struct {
	Bill        database.Bill
	Provisional bool // title still sourced from a votation's Articulo
	Votations   []database.Votation
}

// BillDetail shows one bill (boletín) and every votation taken on it.
func (s *Server) BillDetail(w http.ResponseWriter, req *http.Request) {
	id := chi.URLParam(req, "id")
	bill, err := s.Q.GetBill(req.Context(), id)
	if err != nil {
		s.dbErr(w, req, err)
		return
	}
	votations, err := s.Q.GetVotationsByBill(req.Context(), sql.NullString{String: id, Valid: true})
	if err != nil {
		s.dbErr(w, req, err)
		return
	}
	s.page(w, render.PageBill, BillData{
		Bill:        bill,
		Provisional: bill.TitleSource == "votation_articulo",
		Votations:   votations,
	})
}
