// Equivalence suite for the named-parameter migration at the query seam
// (PR history: the optional-filter queries used to bind the SAME value twice
// as two positional slots, so callers hand-satisfied a ColumnN/typed-field
// coincidence nothing enforced -- passing Column1 "senado" with ChamberID
// "camara" compiled and silently disabled the filter).
//
// The legacy SQL below is pinned verbatim from the pre-migration
// db/queries/{votations,representatives}.sql (merge 99b0843). Each test
// binds the old idiom the way the migrated call sites used to (same value
// in both slots) and requires the generated named-parameter query to return
// the exact same rows everywhere, on a fixture spread across both chambers,
// both results, and NULL + two non-NULL bill links.
package database

import (
	"context"
	"database/sql"
	"sort"
	"testing"
)

const legacyListVotationsIDs = `SELECT v.id
FROM votations v
LEFT JOIN bills b ON b.id = v.bill_id
WHERE (? = '' OR v.chamber_id = ?)
  AND (? = '' OR v.result = ?)
  AND (? = '' OR v.bill_id = ?)
ORDER BY v.date DESC
`

const legacyCountVotations = `SELECT COUNT(*) FROM votations
WHERE (? = '' OR chamber_id = ?)
  AND (? = '' OR result = ?)
  AND (? = '' OR bill_id = ?)
`

const legacyListRepresentativesIDs = `SELECT r.id
FROM representatives r
LEFT JOIN representative_parties rp ON rp.representative_id = r.id AND rp.end_date IS NULL
LEFT JOIN parties p ON p.id = rp.party_id
WHERE (? = '' OR r.chamber_id = ?)
ORDER BY r.active DESC, r.last_name
`

const legacyCountRepresentatives = `SELECT COUNT(*) FROM representatives
WHERE (? = '' OR chamber_id = ?)
`

func seedFilterFixture(t *testing.T, db *sql.DB) {
	t.Helper()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("seed %q: %v", q[:40], err)
		}
	}
	exec(`INSERT INTO bills (id, title) VALUES
		('10001-01', 'Boletin Uno'), ('10002-02', 'Boletin Dos')`)
	// Five votations: two chambers, two results, NULL and non-NULL bill
	// links, dates strictly distinct so ORDER BY date DESC is deterministic.
	exec(`INSERT INTO votations (chamber_id, external_id, bill_id, date, vote_date, result) VALUES
		('camara', '6001', '10001-01', '2025-03-03T10:00:00', '2025-03-03', 'Aprobado'),
		('camara', '6002', '10002-02', '2025-03-02T10:00:00', '2025-03-02', 'Rechazado'),
		('camara', '6003', NULL,      '2025-03-01T10:00:00', '2025-03-01', 'Aprobado'),
		('senado', '6004', '10001-01', '2025-02-28T10:00:00', '2025-02-28', 'Aprobado'),
		('senado', '6005', NULL,       '2025-02-27T10:00:00', '2025-02-27', 'Rechazado')`)
	exec(`INSERT INTO representatives (chamber_id, external_id, first_name, last_name, active) VALUES
		('camara', '7001', 'Ana',   'Fuentes', 1),
		('camara', '7002', 'Beto',  'Garces',  1),
		('senado', '7003', 'Clara', 'Lagos',   0)`)
}

func legacyInts(t *testing.T, db *sql.DB, query string, args ...any) []int64 {
	t.Helper()
	rows, err := db.Query(query, args...)
	if err != nil {
		t.Fatalf("legacy query: %v", err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan legacy row: %v", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("legacy rows: %v", err)
	}
	return ids
}

// Both-side duel: bind the legacy two-slot idiom with the value twice, run
// the generated named query with the same single value, require identical
// row sets.
func assertSameRows(t *testing.T, name string, legacy []int64, generated []int64) {
	t.Helper()
	sort.Slice(legacy, func(i, j int) bool { return legacy[i] < legacy[j] })
	sort.Slice(generated, func(i, j int) bool { return generated[i] < generated[j] })
	if len(legacy) != len(generated) {
		t.Fatalf("%s: %d rows vs %d:\nlegacy %v\ngenerated %v", name, len(legacy), len(generated), legacy, generated)
	}
	for i := range legacy {
		if legacy[i] != generated[i] {
			t.Fatalf("%s: row %d legacy %d != generated %d\nlegacy %v\ngenerated %v",
				name, i, legacy[i], generated[i], legacy, generated)
		}
	}
}

func assertInts(t *testing.T, name string, got, want []int64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: got %v, want %v", name, got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%s: got %v, want %v", name, got, want)
		}
	}
}

func TestListVotationsNamedParamsMatchLegacyBinding(t *testing.T) {
	q, db := testRuleDB(t)
	seedFilterFixture(t, db)
	ctx := context.Background()

	for _, c := range []struct {
		chamber, result, bill string
	}{
		{"", "", ""},
		{"camara", "", ""},
		{"camara", "Aprobado", ""},
		{"camara", "", "10001-01"},
		{"senado", "Rechazado", ""},
		{"", "Aprobado", ""},
		{"", "", "10001-01"},
		{"camara", "Rechazado", "10002-02"},
	} {
		legacyArgs := []any{c.chamber, c.chamber, c.result, c.result, c.bill, c.bill}
		got, err := q.ListVotations(ctx, ListVotationsParams{
			Chamber: c.chamber, Result: c.result, Bill: c.bill, Limit: 100,
		})
		if err != nil {
			t.Fatalf("%+v: %v", c, err)
		}
		generated := make([]int64, len(got))
		for i, r := range got {
			generated[i] = r.ID
		}
		assertSameRows(t, "ListVotations", legacyInts(t, db, legacyListVotationsIDs, legacyArgs...), generated)

		wantCount := legacyInts(t, db, legacyCountVotations, legacyArgs...)
		haveCount, err := q.CountVotations(ctx, CountVotationsParams{
			Chamber: c.chamber, Result: c.result, Bill: c.bill,
		})
		if err != nil {
			t.Fatalf("%+v count: %v", c, err)
		}
		if haveCount != wantCount[0] {
			t.Fatalf("%+v: count %d, legacy says %d", c, haveCount, wantCount[0])
		}
	}
}

// Deterministic expectations spelled out from the fixture (not captured):
// newest first, the bill join resolves, and NULL-bill rows keep a NULL
// title through every filter combination that admits them.
func TestListVotationsFilterGoldens(t *testing.T) {
	q, db := testRuleDB(t)
	seedFilterFixture(t, db)
	ctx := context.Background()

	all, err := q.ListVotations(ctx, ListVotationsParams{Chamber: "", Result: "", Bill: "", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for _, r := range all {
		ids = append(ids, r.ID)
	}
	assertInts(t, "no filters, newest first", ids, []int64{1, 2, 3, 4, 5})

	cam, err := q.ListVotations(ctx, ListVotationsParams{Chamber: "camara", Result: "", Bill: "", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	ids = ids[:0]
	for _, r := range cam {
		ids = append(ids, r.ID)
	}
	assertInts(t, "chamber only", ids, []int64{1, 2, 3})

	bill1, err := q.ListVotations(ctx, ListVotationsParams{Chamber: "", Result: "", Bill: "10001-01", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(bill1) != 2 || bill1[0].ID != 1 || bill1[1].ID != 4 {
		t.Fatalf("bill filter: %+v, want votations 1 then 4", bill1)
	}
	if bill1[0].BillTitle.String != "Boletin Uno" || !bill1[0].BillTitle.Valid {
		t.Fatalf("bill title join: %+v", bill1[0].BillTitle)
	}

	null, err := q.ListVotations(ctx, ListVotationsParams{Chamber: "camara", Result: "Aprobado", Bill: "", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(null) != 2 || null[0].ID != 1 || null[1].ID != 3 {
		t.Fatalf("chamber+result filter: %+v, want votations 1 then 3", null)
	}
	if null[1].BillTitle.Valid {
		t.Fatalf("NULL-bill row must keep NULL title: %+v", null[1].BillTitle)
	}
}

func TestListRepresentativesNamedParamsMatchLegacyBinding(t *testing.T) {
	q, db := testRuleDB(t)
	seedFilterFixture(t, db)
	ctx := context.Background()

	for _, chamber := range []string{"", "camara", "senado"} {
		got, err := q.ListRepresentatives(ctx, ListRepresentativesParams{
			Chamber: chamber, Limit: 100,
		})
		if err != nil {
			t.Fatalf("chamber %q: %v", chamber, err)
		}
		generated := make([]int64, len(got))
		for i, r := range got {
			generated[i] = r.ID
		}
		assertSameRows(t, "ListRepresentatives", legacyInts(t, db, legacyListRepresentativesIDs, chamber, chamber), generated)

		wantCount := legacyInts(t, db, legacyCountRepresentatives, chamber, chamber)
		haveCount, err := q.CountRepresentatives(ctx, chamber)
		if err != nil {
			t.Fatalf("chamber %q count: %v", chamber, err)
		}
		if haveCount != wantCount[0] {
			t.Fatalf("chamber %q: count %d, legacy says %d", chamber, haveCount, wantCount[0])
		}
	}
}

// The generated list keeps its documented order (active first, then last
// name) under the named spelling.
func TestListRepresentativesOrderGolden(t *testing.T) {
	q, db := testRuleDB(t)
	seedFilterFixture(t, db)
	ctx := context.Background()

	got, err := q.ListRepresentatives(ctx, ListRepresentativesParams{Chamber: "", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].ID != 1 || got[1].ID != 2 || got[2].ID != 3 {
		t.Fatalf("order: %v, want active reps 1,2 then 3", got)
	}
}
