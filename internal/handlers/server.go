// Package handlers: chi handlers grouped by resource, read-only.
// The ingest Syncer is the sole write path (docs/DESIGN.md gap 6).
package handlers

import (
	"log/slog"

	"congress-visualizer/internal/analytics"
	"congress-visualizer/internal/database"
	"congress-visualizer/internal/render"
)

// Server wires every handler to read-only queries plus the renderer.
type Server struct {
	Q   *database.Queries
	R   *render.Renderer
	Log *slog.Logger

	// pairCache / rankCache cache the two expensive analytics queries —
	// the ~5s alignment self-join and the ~500k-row loyalty scan — keyed
	// by their window params; entries expire after analyticsCacheTTL so
	// freshly ingested votes appear without a process restart.
	pairCache *ttlCache[[]database.GetPairAgreementRowsRow]
	rankCache *ttlCache[[]analytics.RepLoyalty]
}

// New builds the Server with the render.FuncMap shared with templates.
func New(q *database.Queries, r *render.Renderer, log *slog.Logger) *Server {
	return &Server{
		Q: q, R: r, Log: log,
		pairCache: newTTLCache[[]database.GetPairAgreementRowsRow](analyticsCacheTTL, 64),
		rankCache: newTTLCache[[]analytics.RepLoyalty](analyticsCacheTTL, 32),
	}
}
