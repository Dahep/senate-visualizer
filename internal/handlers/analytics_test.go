package handlers

import (
	"database/sql"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"congress-visualizer/internal/database"
	"congress-visualizer/internal/render"
)

// Phase-2 page tests over a migrated fixture DB (the PR-#3 ingest test
// pattern). They pin the window policy per page — the point of the deep
// window value:
//
//   - party cohesion (and its monthly chart) queries ALL history,
//   - /rebels and /alignment default to the legislature window,
//   - the rep-profile loyalty is explicitly all-history (named policy),
//   - the party page's chart img carries the same bounds the page queried,
//   - malformed ?from/?to is ONE uniform 400 on every page.
//
// Seed: party AAA (id 1) with reps Ana (id 1) and Beto (id 2); votation 1
// casts yes/yes at 2018-01-15 (pre-legislature), votations 2-3 cast no/yes
// and yes/yes at 2024-06/2024-07 (inside the 2022- legislature). The split
// is what makes the window observable: rep loyalty over all history =
// 66.7% (2 of 3), over the legislature window alone = 50.0% (1 of 2).
// (Votations 2-3 must share a two-month chart span: go-chart's line cannot
// render a single-point series — a pre-existing TrendLine quirk, not the
// window's concern.)

func testHandler(t *testing.T) (*Server, *sql.DB, http.Handler) {
	t.Helper()
	odb, err := database.Open(filepath.Join(t.TempDir(), "handlers.db"))
	if err != nil {
		t.Fatalf("open fixture db: %v", err)
	}
	t.Cleanup(func() { odb.Close() })
	if err := odb.Migrate("../../db/migrations"); err != nil {
		t.Fatalf("migrate fixture db: %v", err)
	}
	r, err := render.New(os.DirFS("../../web"), FuncMap())
	if err != nil {
		t.Fatalf("templates: %v", err)
	}
	srv := New(database.New(odb.Raw()), r, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return srv, odb.Raw(), srv.Routes()
}

func seedAnalyticsFixture(t *testing.T, db *sql.DB) {
	t.Helper()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("seed %q: %v", q[:40], err)
		}
	}
	exec(`INSERT INTO parties (short_name, name, color) VALUES ('AAA', 'Partido de prueba', '#FF0000')`)
	exec(`INSERT INTO representatives (chamber_id, external_id, first_name, last_name, active)
		VALUES ('camara', '90001', 'Ana', 'Fuentes', 1), ('camara', '90002', 'Beto', 'Garces', 1)`)
	exec(`INSERT INTO representative_parties (representative_id, party_id, start_date)
		VALUES (1, 1, '2018-01-01'), (2, 1, '2018-01-01')`)
	exec(`INSERT INTO votations (chamber_id, external_id, date, vote_date)
		VALUES ('camara', '5001', '2018-01-15T14:00:00', '2018-01-15'),
		       ('camara', '5002', '2024-06-01T10:00:00', '2024-06-01'),
		       ('camara', '5003', '2024-07-01T10:00:00', '2024-07-01')`)
	exec(`INSERT INTO individual_votes (votation_id, representative_id, vote, vote_raw) VALUES
		(1, 1, 'yes', 'Afirmativo'), (1, 2, 'yes', 'Afirmativo'),
		(2, 1, 'no', 'En Contra'),  (2, 2, 'yes', 'Afirmativo'),
		(3, 1, 'yes', 'Afirmativo'), (3, 2, 'yes', 'Afirmativo')`)
}

// seededHandler builds the routes over one migrated fixture DB with the
// standard window-observable seed.
func seededHandler(t *testing.T) http.Handler {
	t.Helper()
	_, db, h := testHandler(t)
	seedAnalyticsFixture(t, db)
	return h
}

func get(t *testing.T, h http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", target, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func body(w *httptest.ResponseRecorder) string { return w.Body.String() }

// --- Chart/summary parity -----------------------------------------------------

// The party page's chart img must carry the SAME from/to the summary
// queried. Impossible while the img re-pared an empty window.
func TestPartyPageChartCarriesPageWindow(t *testing.T) {
	h := seededHandler(t)
	w := get(t, h, "/parties/1?from=2019-01-01&to=2020-01-31")
	if w.Code != http.StatusOK {
		t.Fatalf("party page = %d: %s", w.Code, body(w))
	}
	src := `/charts/party-cohesion/1?from=2019-01-01&amp;to=2020-01-31`
	if !strings.Contains(body(w), src) {
		t.Fatalf("chart img does not carry the page window;\nwant src %s\nin: %s", src, body(w))
	}
	if !strings.Contains(body(w), "Ventana 2019-01-01") {
		t.Fatalf("summary note missing the window: %s", body(w))
	}
}

// Default (no bounds): the window value is fully open on the summary AND
// the chart link — one value, both sides.
func TestPartyPageDefaultsAllHistory(t *testing.T) {
	h := seededHandler(t)
	w := get(t, h, "/parties/1")
	if w.Code != http.StatusOK {
		t.Fatalf("party page = %d: %s", w.Code, body(w))
	}
	b := body(w)
	wantSrc := `src="/charts/party-cohesion/1"`
	if !strings.Contains(b, wantSrc) {
		t.Fatalf("open window must render a windowless chart link %s in: %s", wantSrc, b)
	}
	if strings.Contains(b, "src=\"/charts/party-cohesion/1?") {
		t.Fatalf("open window must NOT invent bounds for the chart: %s", b)
	}
	if !strings.Contains(b, "Todo el historial ingerido") {
		t.Fatalf("all-history note missing: %s", b)
	}
}

// The chart endpoint re-parses the img's ?from/?to under the same policy, so
// the rendered SVG honors the window end-to-end.
func TestCohesionChartHonorsWindow(t *testing.T) {
	h := seededHandler(t)

	w := get(t, h, "/charts/party-cohesion/1?from=2024-01-01")
	if w.Code != http.StatusOK {
		t.Fatalf("windowed chart = %d: %s", w.Code, body(w))
	}
	if ct := w.Header().Get("Content-Type"); ct != "image/svg+xml" {
		t.Fatalf("chart content type %q", ct)
	}
	if !strings.Contains(body(w), "2024-06") {
		t.Fatalf("windowed chart missing its month label: %.200s", body(w))
	}
	if strings.Contains(body(w), "2018-01") {
		t.Fatalf("windowed chart leaked data outside the window: %.400s", body(w))
	}

	w = get(t, h, "/charts/party-cohesion/1")
	if w.Code != http.StatusOK {
		t.Fatalf("open-window chart = %d", w.Code)
	}
	for _, month := range []string{"2018-01", "2024-06", "2024-07"} {
		if !strings.Contains(body(w), month) {
			t.Fatalf("all-history chart missing %s: %.200s", month, body(w))
		}
	}
}

// --- Per-page default policies --------------------------------------------------

// /rebels and /alignment resolve open bounds to the legislature start, and
// keep explicit bounds.
func TestRankPagesDefaultToLegislature(t *testing.T) {
	h := seededHandler(t)
	for _, page := range []string{"/rebels", "/alignment"} {
		w := get(t, h, page)
		if w.Code != http.StatusOK {
			t.Fatalf("%s = %d: %s", page, w.Code, body(w))
		}
		from := `name="from" value="2022-03-11"`
		if !strings.Contains(body(w), from) {
			t.Fatalf("%s: legislature default missing %s in: %s", page, from, body(w))
		}
		to := `name="to" value=""`
		if !strings.Contains(body(w), to) {
			t.Fatalf("%s: to bound should stay open: %s", page, body(w))
		}
	}

	// Explicit bounds pass through unchanged (no invented default).
	w := get(t, h, "/rebels?from=2019-01-01")
	if w.Code != http.StatusOK {
		t.Fatalf("/rebels explicit = %d: %s", w.Code, body(w))
	}
	if !strings.Contains(body(w), `name="from" value="2019-01-01"`) {
		t.Fatalf("explicit from must survive policy: %s", body(w))
	}
}

// Rep_profile loyalty is the ALL-HISTORY policy, NAMED not accidental —
// distinct from the /rebels legislature window, and deaf to ?from/?to the
// profile page received.
func TestRepresentativeLoyaltyUsesAllHistory(t *testing.T) {
	h := seededHandler(t)

	w := get(t, h, "/representatives/1")
	if w.Code != http.StatusOK {
		t.Fatalf("rep page = %d: %s", w.Code, body(w))
	}
	// All history: loyal in votation 1, disloyal in 2, loyal in 3 → 66.7%.
	if !strings.Contains(body(w), "66.7%") || !strings.Contains(body(w), "2 de 3") {
		t.Fatalf("rep loyalty is not the all-history computation: %s", body(w))
	}

	// The loyalty policy takes no request input at all.
	w = get(t, h, "/representatives/1?from=2030-01-01&to=2040-01-01")
	if w.Code != http.StatusOK {
		t.Fatalf("rep page windowed = %d: %s", w.Code, body(w))
	}
	if !strings.Contains(body(w), "2 de 3") {
		t.Fatalf("loyalty flinched at page params; named policy must ignore them: %s", body(w))
	}
}

// --- Invalid input: one meaning on every page —-----------------------------------

// The same malformed value gets the same uniform 400 regardless of page,
// including the chart route. No page falls back to a different window.
func TestMalformedWindowIsUniformAcrossPages(t *testing.T) {
	h := seededHandler(t)

	var first string
	for _, target := range []string{
		"/rebels?from=abcd-ef-gh",
		"/alignment?from=abcd-ef-gh",
		"/parties/1?from=abcd-ef-gh",
	} {
		w := get(t, h, target)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400 — page silently kept its own default window", target, w.Code)
		}
		if first == "" {
			first = body(w)
			if !strings.Contains(first, `?from="abcd-ef-gh"`) {
				t.Fatalf("400 notice does not name the offending value: %s", first)
			}
			continue
		}
		if body(w) != first {
			t.Fatalf("%s: notice differs across pages:\n%s\n--- vs ---\n%s", target, body(w), first)
		}
	}

	for _, target := range []string{
		"/alignment?to=9999-99-99",      // sentinel the old shape rule passed
		"/parties/1?from=0000-00-00",    // ditto
		"/charts/party-cohesion/1?to=x", // the chart shares the same rule
	} {
		w := get(t, h, target)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400", target, w.Code)
		}
	}
}
