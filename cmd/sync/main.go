// Command sync is the CLI driver over ingest.Syncer (docs gap 6):
//
//	sync -full                     historical Scan (ceiling probe, down sweep)
//	sync -once                     one incremental Tick (also the default)
//	sync -seed-parties parties.json -seed affiliations.json
//	                               SeedParties + SyncDeputies + validated Apply
//	sync -boletin-check 15351-07   enqueue a bill's votation IDs as candidates
//
// Every mode shares the same unit of work and cursors; the Syncer holds the
// single-writer lease for the duration of a run.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"congress-visualizer/internal/config"
	"congress-visualizer/internal/curation"
	"congress-visualizer/internal/database"
	"congress-visualizer/internal/ingest"
	"congress-visualizer/internal/ingest/camara"
)

func main() {
	full := flag.Bool("full", false, "historical scan from the ID ceiling down to the floor")
	once := flag.Bool("once", false, "one incremental tick (default when no other mode is given)")
	seedParties := flag.String("seed-parties", "", "parties registry JSON (short_name/name/color)")
	seed := flag.String("seed", "", "affiliations JSON: SyncDeputies + validated apply")
	boletinCheck := flag.String("boletin-check", "", "enqueue votation candidates for one boletín")
	flag.Parse()

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
	ctx := context.Background()

	switch {
	case *seedParties != "" || *seed != "":
		if err := runSeed(ctx, db, q, syncer, log, *seedParties, *seed); err != nil {
			log.Error("seed", "err", err)
			os.Exit(1)
		}
	case *boletinCheck != "":
		n, err := syncer.EnqueueBoletinCheck(ctx, *boletinCheck)
		if err != nil {
			log.Error("boletin-check", "boletin", *boletinCheck, "err", err)
			os.Exit(1)
		}
		log.Info("candidates enqueued", "boletin", *boletinCheck, "count", n)
	case *full:
		res, err := syncer.Scan(ctx, ingest.ScanDirectionDown)
		if err != nil {
			log.Error("full scan", "err", err)
			os.Exit(1)
		}
		res.Log(log, "full")
	case *once:
		fallthrough
	default:
		res, err := syncer.Tick(ctx)
		if err != nil {
			log.Error("tick", "err", err)
			os.Exit(1)
		}
		res.Log(log, "once")
	}
}

// runSeed loads the parties registry, refreshes deputies, and applies the
// affiliations file — all validated at the boundary (docs gap 5).
func runSeed(ctx context.Context, db *database.OpenDB, q *database.Queries, syncer *ingest.Syncer, log *slog.Logger, partiesPath, affiliationsPath string) error {
	if partiesPath == "" {
		partiesPath = "data/parties.json"
	}

	// Parties registry: validate the file alone, then upsert.
	var parties []curation.Party
	body, err := os.ReadFile(partiesPath)
	if err != nil {
		return fmt.Errorf("parties: %w", err)
	}
	if err := json.Unmarshal(body, &parties); err != nil {
		return fmt.Errorf("parties: %w", err)
	}
	if problems := curation.Validate(curation.AffiliationsFile{Parties: parties}); len(problems) > 0 {
		return &curation.ValidationError{Problems: problems}
	}
	if err := curation.SeedParties(ctx, q, curation.AffiliationsFile{Parties: parties}); err != nil {
		return fmt.Errorf("seed parties: %w", err)
	}
	log.Info("parties seeded", "count", len(parties))

	if affiliationsPath == "" {
		return nil // registry-only seed
	}

	file, err := curation.LoadAffiliations(partiesPath, affiliationsPath)
	if err != nil {
		return err
	}
	if problems := curation.Validate(file); len(problems) > 0 {
		return &curation.ValidationError{Problems: problems}
	}

	n, err := syncer.SyncDeputies(ctx)
	if err != nil {
		return fmt.Errorf("sync deputies: %w", err)
	}
	log.Info("deputies synced", "count", n)

	if err := curation.Apply(ctx, db.Raw(), q, file); err != nil {
		return fmt.Errorf("apply affiliations: %w", err)
	}
	log.Info("affiliations applied", "count", len(file.Affiliations))
	return nil
}
