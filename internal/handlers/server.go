// Package handlers: chi handlers grouped by resource, read-only.
// The ingest Syncer is the sole write path (docs/DESIGN.md gap 6).
package handlers

import (
	"log/slog"

	"congress-visualizer/internal/database"
	"congress-visualizer/internal/render"
)

// Server wires every handler to read-only queries plus the renderer.
type Server struct {
	Q   *database.Queries
	R   *render.Renderer
	Log *slog.Logger
}

// New builds the Server with the render.FuncMap shared with templates.
func New(q *database.Queries, r *render.Renderer, log *slog.Logger) *Server {
	return &Server{Q: q, R: r, Log: log}
}
