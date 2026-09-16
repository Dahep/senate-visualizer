// Command server is the web app: read-only chi handlers plus the background
// incremental-sync scheduler (one of the two Syncer drivers; docs gap 6).
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"congress-visualizer/internal/config"
	"congress-visualizer/internal/database"
	"congress-visualizer/internal/handlers"
	"congress-visualizer/internal/ingest"
	"congress-visualizer/internal/ingest/camara"
	"congress-visualizer/internal/render"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	cfg := config.Load()
	if err := os.MkdirAll(filepath.Dir(cfg.DBPath), 0o755); err != nil {
		log.Error("data dir", "err", err)
		os.Exit(1)
	}
	db, err := database.Open(cfg.DBPath)
	if err != nil {
		log.Error("open db", "err", err)
		os.Exit(1)
	}
	defer db.Close()
	if err := db.Migrate(cfg.MigrationsDir); err != nil {
		log.Error("migrate", "err", err)
		os.Exit(1)
	}
	q := database.New(db.Raw())

	client := camara.NewClient(cfg.APIBaseURL, cfg.APIDelay)
	syncer := ingest.NewSyncer(db.Raw(), q, client, log)

	// Templates walk web/templates from disk on every boot (dev-friendly).
	r, err := render.New(os.DirFS("web"), handlers.FuncMap())
	if err != nil {
		log.Error("templates", "err", err)
		os.Exit(1)
	}
	srv := handlers.New(q, r, log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Background scheduler: first tick 10s after boot (let the server settle),
	// then every SyncInterval. The Syncer acquires the single-writer lease
	// internally; while a CLI scan holds it, Tick is skipped with a warning.
	go func() {
		first := time.NewTimer(10 * time.Second)
		defer first.Stop()
		select {
		case <-ctx.Done():
			return
		case <-first.C:
			tick(ctx, syncer, log)
		}
		t := time.NewTicker(cfg.SyncInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				tick(ctx, syncer, log)
			}
		}
	}()

	httpServer := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()

	log.Info("listening", "addr", httpServer.Addr)
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("http", "err", err)
		os.Exit(1)
	}
}

// tick runs one incremental sync and logs the result; a busy lease is a
// skip, not a failure.
func tick(ctx context.Context, s *ingest.Syncer, log *slog.Logger) {
	res, err := s.Tick(ctx)
	if err != nil {
		log.Warn("sync tick skipped", "err", err)
		return
	}
	res.Log(log, "tick")
}
