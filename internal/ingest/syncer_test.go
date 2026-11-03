package ingest

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"congress-visualizer/internal/database"
	"congress-visualizer/internal/ingest/camara"
)

// Shared fixture for the Syncer-level regression tests (lease, roster,
// bill title): a migrated fixture DB plus a Syncer whose client is a stub
// and whose lease timings are injectable (the PR #3 candidates_test.go
// pattern: real migrations, no network).
func newTestSyncer(t *testing.T) (*Syncer, *sql.DB) {
	t.Helper()
	odb, err := database.Open(filepath.Join(t.TempDir(), "syncer.db"))
	if err != nil {
		t.Fatalf("open fixture db: %v", err)
	}
	t.Cleanup(func() { odb.Close() })
	if err := odb.Migrate("../../db/migrations"); err != nil {
		t.Fatalf("migrate fixture db: %v", err)
	}
	db := odb.Raw()
	return &Syncer{
		db:            db,
		q:             database.New(db),
		client:        stubAPIClient{},
		log:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		driver:        "test-driver",
		leaseTTL:      time.Minute,
		leaseRenewIn:  15 * time.Second,
		rosterQueries: func(tx *sql.Tx) *database.Queries { return database.New(db).WithTx(tx) },
	}, db
}

// stubAPIClient doubles camaraAPI: canned roster responses; the votation
// endpoints are never exercised (tests drive upsertVotation directly).
type stubAPIClient struct {
	vig    []camara.DiputadoXML
	all    []camara.DiputadoXML
	vigErr error
	allErr error
}

func (c stubAPIClient) VotacionDetalle(context.Context, int) (camara.VotacionXML, error) {
	return camara.VotacionXML{}, camara.ErrNotFound
}

func (c stubAPIClient) BoletinVotaciones(context.Context, string) ([]camara.VotacionXML, error) {
	return nil, nil
}

func (c stubAPIClient) DiputadosVigentes(context.Context) ([]camara.DiputadoXML, error) {
	return c.vig, c.vigErr
}

func (c stubAPIClient) Diputados(context.Context) ([]camara.DiputadoXML, error) {
	return c.all, c.allErr
}

// --- Single-writer lease ------------------------------------------------------

// SQLite compares the stored lease expiry against strftime('%s','now') — the
// wall clock, at one-second granularity — so these tests must too: no fake
// clock, TTLs small enough to sleep past.

// Regression for the fixed 2h expiry: a third party steals an expired lease
// but NEVER a live one. (Old bug: expiry was hardcoded time.Now()+2h, shorter
// than the ~3.6h historical scan, so the server's scheduled Tick stole a
// running cmd/sync's lease mid-sweep.)
func TestLeaseStealFiresOnlyAfterExpiry(t *testing.T) {
	s, _ := newTestSyncer(t)
	s.leaseTTL = 1 * time.Second

	ctx := context.Background()
	if ok, err := s.AcquireSyncLock(ctx, DriverCmdSync); err != nil || !ok {
		t.Fatalf("first acquire: ok=%v err=%v", ok, err)
	}
	if ok, err := s.AcquireSyncLock(ctx, DriverScheduler); ok || err != nil {
		t.Fatalf("third party acquired a live lease: ok=%v err=%v", ok, err)
	}
	// expiry = acquire-second + 1s; the CAS steals when expiry < strftime
	// (now), so sleep past that second.
	time.Sleep(2 * time.Second)
	if ok, err := s.AcquireSyncLock(ctx, DriverScheduler); !ok || err != nil {
		t.Fatalf("dead lease not stealable: ok=%v err=%v", ok, err)
	}
}

// Renewal keeps a live lease unstealable: the holder re-CASes on interval,
// moving the expiry forward, so the steal window never opens while the run
// is alive. This is the mutation target for holdLease's renewal loop.
func TestHoldLeaseRenewalBeatsExpiry(t *testing.T) {
	s, _ := newTestSyncer(t)
	s.leaseTTL = 1 * time.Second
	s.leaseRenewIn = 200 * time.Millisecond

	ctx := context.Background()
	l, err := s.holdLease(ctx)
	if err != nil {
		t.Fatalf("hold lease: %v", err)
	}
	t.Cleanup(func() { s.release(l) })

	// Let several TTLs elapse on the wall clock; the renewal loop must keep
	// moving the expiry so a third party never sees a dead lease.
	time.Sleep(2 * time.Second)
	if ok, err := s.AcquireSyncLock(ctx, DriverScheduler); ok || err != nil {
		t.Fatalf("third party took the renewed lease: ok=%v err=%v", ok, err)
	}

	s.release(l)
	if ok, err := s.AcquireSyncLock(ctx, DriverScheduler); !ok || err != nil {
		t.Fatalf("released lease not re-acquirable: ok=%v err=%v", ok, err)
	}
}

// Same-owner CAS: a holder may refresh its own lease (live or expired) — the
// one legitimate way to extend the expiry — while a different owner cannot.
func TestLeaseSameOwnerCASRefresh(t *testing.T) {
	s, db := newTestSyncer(t)
	s.leaseTTL = 1 * time.Second

	ctx := context.Background()
	if ok, err := s.AcquireSyncLock(ctx, DriverCmdSync); err != nil || !ok {
		t.Fatalf("acquire: ok=%v err=%v", ok, err)
	}
	if owner := leaseOwner(t, db); owner != DriverCmdSync {
		t.Fatalf("lease owner = %q, want %q", owner, DriverCmdSync)
	}
	// Renewal while LIVE: same owner re-CASes, third party refused.
	if ok, err := s.AcquireSyncLock(ctx, DriverCmdSync); err != nil || !ok {
		t.Fatalf("same-owner renewal of a live lease: ok=%v err=%v", ok, err)
	}
	if ok, err := s.AcquireSyncLock(ctx, DriverScheduler); ok || err != nil {
		t.Fatalf("third party acquired a renewed live lease: ok=%v err=%v", ok, err)
	}
	// Refresh PAST expiry: the dead lease belongs to this owner, so it keeps
	// re-claiming its own run.
	time.Sleep(2 * time.Second)
	if ok, err := s.AcquireSyncLock(ctx, DriverCmdSync); err != nil || !ok {
		t.Fatalf("same-owner refresh past expiry: ok=%v err=%v", ok, err)
	}
	if ok, err := s.AcquireSyncLock(ctx, DriverScheduler); ok || err != nil {
		t.Fatalf("third party acquired a freshly refreshed lease: ok=%v err=%v", ok, err)
	}
}

func leaseOwner(t *testing.T, db *sql.DB) string {
	t.Helper()
	var v string
	if err := db.QueryRow(`SELECT value FROM sync_metadata WHERE key = 'camara.sync_lock'`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	owner, _, _ := strings.Cut(v, "|")
	return owner
}
