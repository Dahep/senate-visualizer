package handlers

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"congress-visualizer/internal/render"
)

// pageSize is the fixed list-page size (docs: page param >= 1, size 25).
const pageSize = 25

// Routes mounts every route of the MVP on a chi router. Handlers are
// read-only; the ingest Syncer is the sole write path.
func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()

	r.Get("/", s.Home)
	r.Get("/votations", s.Votations)
	r.Get("/votations/{id}", s.VotationDetail)
	r.Get("/representatives", s.Representatives)
	r.Get("/representatives/{id}", s.RepresentativeDetail)
	r.Get("/parties", s.Parties)
	r.Get("/parties/{id}", s.PartyDetail)
	r.Get("/bills", s.Bills)
	r.Get("/bills/{id}", s.BillDetail)

	// Phase-2 analytics pages.
	r.Get("/rebels", s.Rebels)
	r.Get("/alignment", s.Alignment)

	// HTMX partial + standalone SVG charts (image/svg+xml; never inlined).
	r.Get("/partials/vote-breakdown/{id}", s.VoteBreakdownPartial)
	r.Get("/charts/party-breakdown/{id}", s.PartyBreakdownChart)
	r.Get("/charts/party-cohesion/{id}", s.PartyCohesionChart)

	// Static assets (css; js/ intentionally empty — zero authored JS).
	r.Handle("/static/*", http.StripPrefix("/static/",
		http.FileServer(http.Dir("web/static"))))

	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		http.Error(w, "404 — página no encontrada", http.StatusNotFound)
	})
	return r
}

// Pagination carries the list-page navigation state to templates.
type Pagination struct {
	Page  int64
	Size  int64
	Total int64
}

func newPagination(page, total int64) Pagination {
	return Pagination{Page: page, Size: pageSize, Total: total}
}

func (p Pagination) HasPrev() bool { return p.Page > 1 }
func (p Pagination) HasNext() bool { return p.Page*p.Size < p.Total }
func (p Pagination) PrevPage() int64 {
	if p.Page > 1 {
		return p.Page - 1
	}
	return 1
}
func (p Pagination) NextPage() int64 { return p.Page + 1 }

// Pages is the total page count (at least 1 so "página 1 de 1" renders).
func (p Pagination) Pages() int64 {
	if p.Total <= 0 {
		return 1
	}
	return (p.Total + p.Size - 1) / p.Size
}

func (p Pagination) offset() int64 { return (p.Page - 1) * p.Size }

// pageParam parses ?page= clamped to >= 1.
func pageParam(req *http.Request) int64 {
	p, err := strconv.ParseInt(req.URL.Query().Get("page"), 10, 64)
	if err != nil || p < 1 {
		return 1
	}
	return p
}

// idParam parses a chi {id} path parameter; ok=false means malformed.
func idParam(req *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(req, "id"), 10, 64)
	return id, err == nil
}

// page renders a full page, logging a broken render (headers may already be
// sent, so there is nothing left to serve to the client).
func (s *Server) page(w http.ResponseWriter, p render.Page, data any) {
	if err := s.R.Page(w, p, data); err != nil {
		s.Log.Error("render page", "page", string(p), "err", err)
	}
}

// dbErr sends 404 for a missing row, 500 for anything else.
func (s *Server) dbErr(w http.ResponseWriter, req *http.Request, err error) {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		http.NotFound(w, req)
	default:
		s.Log.Error("db", "path", req.URL.Path, "err", err)
		http.Error(w, "error interno", http.StatusInternalServerError)
	}
}
