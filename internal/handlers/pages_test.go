// Page-render regression for the named-parameter migration: / (headline
// counts + latest 10), /votations and /representatives keep querying the
// camara-only optional filter the migrated call sites set, so their
// observable content over this fixture must stay exactly the camara rows.
package handlers

import (
	"database/sql"
	"net/http"
	"strings"
	"testing"
)

// Fixtures: two camara votations (distinct subjects, one NULL bill link)
// and one senado votation. The migrated handlers hardcode Chamber "camara",
// so votation 3 (senado) must not render anywhere on these pages.
func seedPagesFixture(t *testing.T, db *sql.DB) {
	t.Helper()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("seed %q: %v", q[:40], err)
		}
	}
	exec(`INSERT INTO bills (id, title) VALUES
		('10001-01', 'Boletin Uno')`)
	exec(`INSERT INTO representatives (chamber_id, external_id, first_name, last_name, active) VALUES
		('camara', '7001', 'Ana',   'Fuentes', 1),
		('camara', '7002', 'Beto',  'Garces',  1),
		('senado', '7003', 'Clara', 'Lagos',   0)`)
	exec(`INSERT INTO votations (chamber_id, external_id, bill_id, date, vote_date, result, subject) VALUES
		('camara', '6001', '10001-01', '2025-03-02T10:00:00', '2025-03-02', 'Aprobado',  'Busca de comision'),
		('camara', '6002', NULL,       '2025-03-01T10:00:00', '2025-03-01', 'Rechazado', NULL),
		('senado', '6003', '10001-01', '2025-02-28T10:00:00', '2025-02-28', 'Aprobado',  'Tercera de senado')`)
}

// The votations and representatives list pages stay chamber-scoped under
// the named-parameter spelling: every camara row renders (its NULL-bill
// row as "(sin materia)"), the senado row and its links never render, and
// the votations order stays newest-first.
func TestListPagesStayChamberScopedAfterNamedParams(t *testing.T) {
	_, db, h := testHandler(t)
	seedPagesFixture(t, db)

	w := get(t, h, "/votations")
	if w.Code != http.StatusOK {
		t.Fatalf("/votations = %d: %s", w.Code, body(w))
	}
	page := body(w)
	for _, want := range []string{"Busca de comision", "(sin materia)"} {
		if !strings.Contains(page, want) {
			t.Fatalf("/votations missing %q:\n%s", want, page)
		}
	}
	for _, none := range []string{"Tercera de senado", "6003"} {
		if strings.Contains(page, none) {
			t.Fatalf("/votations leaked senado content %q:\n%s", none, page)
		}
	}
	// Newest first: votation 1's row must come before votation 2's.
	if first, last := strings.Index(page, "Busca de comision"), strings.Index(page, "(sin materia)"); first == -1 || last == -1 || first > last {
		t.Fatalf("/votations order broken (newest first):\n%s", page)
	}

	w = get(t, h, "/representatives")
	if w.Code != http.StatusOK {
		t.Fatalf("/representatives = %d: %s", w.Code, body(w))
	}
	page = body(w)
	for _, want := range []string{"Fuentes", "Garces"} {
		if !strings.Contains(page, want) {
			t.Fatalf("/representatives missing %q:\n%s", want, page)
		}
	}
	for _, none := range []string{"Lagos"} {
		if strings.Contains(page, none) {
			t.Fatalf("/representatives leaked senado content %q:\n%s", none, page)
		}
	}
	// Active reps first (both seeded camara reps are active): Fuentes
	// before Garces regardless of surname order.
	if first, last := strings.Index(page, "Fuentes"), strings.Index(page, "Garces"); first == -1 || last == -1 || first > last {
		t.Fatalf("/representatives order broken:\n%s", page)
	}
}

// The home headline counts AND the latest-10 strip run the same migrated
// queries: the votation/rep counts stay camara-scoped (3 seeded rows, but
// 2 visible), and the latest strip heads with votation 6001, never the
// senado row.
func TestHomePageCountsAndLatestStayChamberScoped(t *testing.T) {
	_, db, h := testHandler(t)
	seedPagesFixture(t, db)

	w := get(t, h, "/")
	if w.Code != http.StatusOK {
		t.Fatalf("/ = %d: %s", w.Code, body(w))
	}
	page := body(w)
	if !strings.Contains(page, `href="/votations"><span class="stat-n">2</span>`) {
		t.Fatalf("/ votation count not camara-scoped to 2:\n%s", page)
	}
	if !strings.Contains(page, `href="/representatives"><span class="stat-n">2</span>`) {
		t.Fatalf("/ representative count not camara-scoped to 2:\n%s", page)
	}
	for _, none := range []string{"Tercera de senado", "6003"} {
		if strings.Contains(page, none) {
			t.Fatalf("/ leaked senado content %q:\n%s", none, page)
		}
	}
	for _, want := range []string{"Busca de comision"} {
		if !strings.Contains(page, want) {
			t.Fatalf("/ latest strip missing %q:\n%s", want, page)
		}
	}
}
