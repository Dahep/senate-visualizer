// Regression suite for the ONE party-at-date spelling (migration 003's
// rep_party_at_date view). Every consumer query's golden output was captured
// BEFORE the view landed, when the rule was hand-written in six sites across
// four query files in four formulations (direct joins in analytics.sql, two
// EXISTS inside GetPairAgreementRows, scalar subqueries with a special-cased
// start bound in GetVotesByVotation) - all six agreed on this fixture, so the
// goldens prove the view changes nothing observable.
//
// The fixture pins every boundary trap the rule must honor:
//   - an end_date exactly == vote_date still covers (V2/R1);
//   - a start_date exactly == vote_date covers (V4/R2);
//   - sequential spans in one membership window (R1) with a coverage GAP
//     between them (V3's 2020-07-01 belongs to neither span -> unattributed);
//   - a fully unaffiliated rep renders IND-less unaffiliated (R3);
//   - the '0000-00-00' sentinel vote_date (V5) resolves as unaffiliated,
//     never "covered".
package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"sort"
	"testing"
)

func testRuleDB(t *testing.T) (*Queries, *sql.DB) {
	t.Helper()
	odb, err := Open(filepath.Join(t.TempDir(), "rule.db"))
	if err != nil {
		t.Fatalf("open fixture db: %v", err)
	}
	t.Cleanup(func() { odb.Close() })
	if err := odb.Migrate("../../db/migrations"); err != nil {
		t.Fatalf("migrate fixture db: %v", err)
	}
	db := odb.Raw()
	return New(db), db
}

func seedPartyAtDateFixture(t *testing.T, db *sql.DB) {
	t.Helper()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatalf("seed %q: %v", query[:40], err)
		}
	}
	exec(`INSERT INTO parties (short_name, name, color) VALUES
		('PAAA', 'Prueba A', '#FF0000'), ('PBBB', 'Prueba B', '#00FF00')`)
	exec(`INSERT INTO representatives (chamber_id, external_id, first_name, last_name, active) VALUES
		('camara', '9001', 'Ana', 'Fuentes', 1),
		('camara', '9002', 'Beto', 'Garces', 1),
		('camara', '9003', 'Clara', 'Lagos', 1)`)
	// R1: sequential spans WITH a coverage gap between them; end boundary
	// exactly on V2's vote_date (inclusive), open end after the gap.
	exec(`INSERT INTO representative_parties (representative_id, party_id, start_date, end_date) VALUES
		(1, 1, '2018-01-01', '2020-06-30'), (1, 2, '2021-01-01', NULL)`)
	// R2: start exactly == V4's vote_date; open end.
	exec(`INSERT INTO representative_parties (representative_id, party_id, start_date) VALUES
		(2, 1, '2019-05-05')`)
	exec(`INSERT INTO votations (chamber_id, external_id, date, vote_date) VALUES
		('camara', '5001', '2019-12-31T10:00:00', '2019-12-31'),
		('camara', '5002', '2020-06-30T10:00:00', '2020-06-30'),
		('camara', '5003', '2020-07-01T10:00:00', '2020-07-01'),
		('camara', '5004', '2019-05-05T10:00:00', '2019-05-05'),
		('camara', '5005', '0000-00-00T00:00:00', '0000-00-00')`)
	exec(`INSERT INTO individual_votes (votation_id, representative_id, vote, vote_raw) VALUES
		(1, 1, 'yes', 'Afirmativo'), (1, 2, 'no', 'En Contra'), (1, 3, 'abstain', 'Abstencion'),
		(2, 1, 'yes', 'Afirmativo'), (2, 2, 'abstain', 'Abstencion'),
		(3, 1, 'no', 'En Contra'), (3, 2, 'yes', 'Afirmativo'),
		(4, 2, 'yes', 'Afirmativo'),
		(5, 2, 'yes', 'Afirmativo'), (5, 3, 'no', 'En Contra')`)
}

// canon renders query rows OR one golden literal, as sorted JSON lines with
// alphabetic keys (the round trip re-keys struct-ordered captures and goldens
// alike), so GROUP BY orders and capture error cannot leak into comparison.
func canon(t *testing.T, v any) []string {
	t.Helper()

	rowJSON := func(b any) string {
		rb, err := json.Marshal(b)
		if err != nil {
			t.Fatalf("marshal %v: %v", b, err)
		}
		return string(rb)
	}

	var rows []any
	if lines, ok := v.([]string); ok { // a golden literal list
		for _, l := range lines {
			var m map[string]any
			if err := json.Unmarshal([]byte(l), &m); err != nil {
				t.Fatalf("golden literal %s: %v", l, err)
			}
			rows = append(rows, m)
		}
	} else {
		body, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal %T: %v", v, err)
		}
		if err := json.Unmarshal(body, &rows); err != nil {
			t.Fatalf("unmarshal %s: %v", body, err)
		}
	}

	var lines []string
	for _, r := range rows {
		lines = append(lines, rowJSON(r))
	}
	sort.Strings(lines)
	return lines
}

func assertGolden(t *testing.T, name string, rows any, want []string) {
	t.Helper()
	got := canon(t, rows)
	wl := canon(t, want)
	if len(got) != len(wl) {
		t.Fatalf("%s: %d rows, want %d:\n got\n%v\nwant\n%v", name, len(got), len(wl), got, wl)
	}
	for i, g := range got {
		if g != wl[i] {
			t.Fatalf("%s row %d:\n got %s\nwant %s", name, i, g, wl[i])
		}
	}
}

// goldens captured by the pre-view baseline dump (deleted; values verbatim
// through the rep_party_at_date view, migration 003). Re-pinned when
// cast_votes (migration 004) replaced the four hand-written IN-lists:
// values unchanged; three shape edits only (cohesion rows lost the never-
// read short_name, loyalty rows lost the never-read vote_date, and the
// provably non-null same re-typed from sql.NullFloat64 to float64).
var goldens = map[string][]string{
	// V1: R1 yes (PAAA covers 2019-12-31), R2 no (start 2019-05-05 covers),
	// R3 abstain (unaffiliated - not rendered here).
	"GetPartyVoteCounts.v1": {
		`{"party_id":1,"short_name":"PAAA","party_name":"Prueba A","color":"#FF0000","vote":"no","n":1}`,
		`{"party_id":1,"short_name":"PAAA","party_name":"Prueba A","color":"#FF0000","vote":"yes","n":1}`,
	},
	// V3: R1's vote is IN the R1 coverage gap -> no PAAA row for its 'no'.
	"GetCohesionRows.all": {
		`{"party_id":1,"votation_id":1,"vote_date":"2019-12-31","vote":"no","n":1}`,
		`{"party_id":1,"votation_id":1,"vote_date":"2019-12-31","vote":"yes","n":1}`,
		`{"party_id":1,"votation_id":2,"vote_date":"2020-06-30","vote":"abstain","n":1}`,
		`{"party_id":1,"votation_id":2,"vote_date":"2020-06-30","vote":"yes","n":1}`,
		`{"party_id":1,"votation_id":3,"vote_date":"2020-07-01","vote":"yes","n":1}`,
		`{"party_id":1,"votation_id":4,"vote_date":"2019-05-05","vote":"yes","n":1}`,
	},
	"GetCohesionRows.from2020": {
		`{"party_id":1,"votation_id":2,"vote_date":"2020-06-30","vote":"abstain","n":1}`,
		`{"party_id":1,"votation_id":2,"vote_date":"2020-06-30","vote":"yes","n":1}`,
		`{"party_id":1,"votation_id":3,"vote_date":"2020-07-01","vote":"yes","n":1}`,
	},
	"GetLoyaltyRows.all": {
		`{"rep_id":1,"first_name":"Ana","last_name":"Fuentes","second_last_name":"","party_id":1,"party_short":"PAAA","votation_id":1,"vote":"yes"}`,
		`{"rep_id":1,"first_name":"Ana","last_name":"Fuentes","second_last_name":"","party_id":1,"party_short":"PAAA","votation_id":2,"vote":"yes"}`,
		`{"rep_id":2,"first_name":"Beto","last_name":"Garces","second_last_name":"","party_id":1,"party_short":"PAAA","votation_id":1,"vote":"no"}`,
		`{"rep_id":2,"first_name":"Beto","last_name":"Garces","second_last_name":"","party_id":1,"party_short":"PAAA","votation_id":2,"vote":"abstain"}`,
		`{"rep_id":2,"first_name":"Beto","last_name":"Garces","second_last_name":"","party_id":1,"party_short":"PAAA","votation_id":3,"vote":"yes"}`,
		`{"rep_id":2,"first_name":"Beto","last_name":"Garces","second_last_name":"","party_id":1,"party_short":"PAAA","votation_id":4,"vote":"yes"}`,
	},
	"GetLoyaltyRows.2020_2021": {
		`{"rep_id":1,"first_name":"Ana","last_name":"Fuentes","second_last_name":"","party_id":1,"party_short":"PAAA","votation_id":2,"vote":"yes"}`,
		`{"rep_id":2,"first_name":"Beto","last_name":"Garces","second_last_name":"","party_id":1,"party_short":"PAAA","votation_id":2,"vote":"abstain"}`,
		`{"rep_id":2,"first_name":"Beto","last_name":"Garces","second_last_name":"","party_id":1,"party_short":"PAAA","votation_id":3,"vote":"yes"}`,
	},
	// V1: joint+abstain rows -> R1xR2 joint 3 (V1,V2,R3-vote? no: joint counts
	// shared CAST votations... V1 yes/no, V2 yes(no cast for R3) - see
	// baseline capture for the verbatim pre-view values).
	"GetPairAgreementRows.all": {
		`{"a_id":1,"b_id":2,"joint":3,"same":0}`,
		`{"a_id":1,"b_id":3,"joint":1,"same":0}`,
		`{"a_id":2,"b_id":3,"joint":2,"same":0}`,
	},
	"GetPairAgreementRows.party1": {
		`{"a_id":1,"b_id":2,"joint":2,"same":0}`,
	},
	"GetPairAgreementRows.party2": {},
	"GetVotesByVotation.v1": {
		`{"vote":"yes","vote_raw":"Afirmativo","rep_id":1,"external_id":"9001","first_name":"Ana","last_name":"Fuentes","second_last_name":"","party_short":"PAAA","party_name":"Prueba A","party_color":"#FF0000","affiliated":true}`,
		`{"vote":"no","vote_raw":"En Contra","rep_id":2,"external_id":"9002","first_name":"Beto","last_name":"Garces","second_last_name":"","party_short":"PAAA","party_name":"Prueba A","party_color":"#FF0000","affiliated":true}`,
		`{"vote":"abstain","vote_raw":"Abstencion","rep_id":3,"external_id":"9003","first_name":"Clara","last_name":"Lagos","second_last_name":"","party_short":"sin-partido","party_name":"Sin partido","party_color":"#808080","affiliated":false}`,
	},
	// The '0000-00-00' sentinel votation: even a covered rep (R2) renders
	// unaffiliated - "surface it, never misattribute it" (schema comment).
	"GetVotesByVotation.v5": {
		`{"vote":"yes","vote_raw":"Afirmativo","rep_id":2,"external_id":"9002","first_name":"Beto","last_name":"Garces","second_last_name":"","party_short":"sin-partido","party_name":"Sin partido","party_color":"#808080","affiliated":false}`,
		`{"vote":"no","vote_raw":"En Contra","rep_id":3,"external_id":"9003","first_name":"Clara","last_name":"Lagos","second_last_name":"","party_short":"sin-partido","party_name":"Sin partido","party_color":"#808080","affiliated":false}`,
	},
}

// The pre-view baseline: every value the SIX hand-written spellings produced
// on this fixture, still produced by the one view spelling.
func TestPartyAtDateGoldenMatchesPreViewBaseline(t *testing.T) {
	q, db := testRuleDB(t)
	seedPartyAtDateFixture(t, db)
	ctx := context.Background()

	party, err := q.GetPartyVoteCounts(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	assertGolden(t, "GetPartyVoteCounts.v1", party, goldens["GetPartyVoteCounts.v1"])

	for _, c := range []struct {
		name string
		args GetCohesionRowsParams
		gold string
	}{
		{"GetCohesionRows.all", GetCohesionRowsParams{Chamber: "camara", FromDate: "", ToDate: "", PartyID: 0}, "GetCohesionRows.all"},
		{"GetCohesionRows.from2020", GetCohesionRowsParams{Chamber: "camara", FromDate: "2020-01-01", ToDate: "", PartyID: 0}, "GetCohesionRows.from2020"},
	} {
		got, err := q.GetCohesionRows(ctx, c.args)
		if err != nil {
			t.Fatal(err)
		}
		assertGolden(t, c.name, got, goldens[c.gold])
	}

	for _, l := range []struct {
		name string
		args GetLoyaltyRowsParams
		gold string
	}{
		{"GetLoyaltyRows.all", GetLoyaltyRowsParams{Chamber: "camara", FromDate: "", ToDate: ""}, "GetLoyaltyRows.all"},
		{"GetLoyaltyRows.2020_2021", GetLoyaltyRowsParams{Chamber: "camara", FromDate: "2020-01-01", ToDate: "2021-12-31"}, "GetLoyaltyRows.2020_2021"},
	} {
		got, err := q.GetLoyaltyRows(ctx, l.args)
		if err != nil {
			t.Fatal(err)
		}
		assertGolden(t, l.name, got, goldens[l.gold])
	}

	for _, p := range []struct {
		name string
		args GetPairAgreementRowsParams
		gold string
	}{
		{"GetPairAgreementRows.all", GetPairAgreementRowsParams{Chamber: "camara", FromDate: "", ToDate: "", PartyID: 0}, "GetPairAgreementRows.all"},
		{"GetPairAgreementRows.party1", GetPairAgreementRowsParams{Chamber: "camara", FromDate: "", ToDate: "", PartyID: 1}, "GetPairAgreementRows.party1"},
		{"GetPairAgreementRows.party2", GetPairAgreementRowsParams{Chamber: "camara", FromDate: "", ToDate: "", PartyID: 2}, "GetPairAgreementRows.party2"},
	} {
		got, err := q.GetPairAgreementRows(ctx, p.args)
		if err != nil {
			t.Fatal(err)
		}
		assertGolden(t, p.name, got, goldens[p.gold])
	}

	votes1, err := q.GetVotesByVotation(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	assertGolden(t, "GetVotesByVotation.v1", votes1, goldens["GetVotesByVotation.v1"])

	votes5, err := q.GetVotesByVotation(ctx, 5)
	if err != nil {
		t.Fatal(err)
	}
	assertGolden(t, "GetVotesByVotation.v5", votes5, goldens["GetVotesByVotation.v5"])
}

// The view itself, on the boundary traps the six spellings only promised:
// inclusive end boundary and exact start boundary both attribute.
func TestViewCoversInclusiveBoundaries(t *testing.T) {
	q, db := testRuleDB(t)
	seedPartyAtDateFixture(t, db)
	ctx := context.Background()

	// end_date == vote_date: V2 (2020-06-30) still attributes R1 to PAAA.
	v, err := q.GetPartyVoteCounts(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(v) == 0 {
		t.Fatal("V2 on the exact end day attributed nothing")
	}
	for _, row := range v {
		if row.PartyID != 1 {
			t.Fatalf("V2 on the exact end day: %v, want party 1 only", v)
		}
	}
	// start_date == vote_date: V4 (2019-05-05) attributes R2 to PAAA.
	v, err = q.GetPartyVoteCounts(ctx, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(v) != 1 || v[0].PartyID != 1 {
		t.Fatalf("V4 on the exact start day: %+v, want party 1, yes 1", v)
	}
}

// AffiliationGaps IS the repair: deputies with at least one vote the rule
// does not cover. R1's V3 vote falls in its span gap, R3 never has a row;
// R2 is covered everywhere real, and the '0000-00-00' sentinel votation
// (V5) never counts as a gap (rendered unaffiliated by ingest design, not
// by missing membership).
func TestAffiliationGapsAreCoverageGaps(t *testing.T) {
	q, db := testRuleDB(t)
	seedPartyAtDateFixture(t, db)
	ctx := context.Background()

	gaps, err := q.AffiliationGaps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(gaps) != 2 || gaps[0] != 1 || gaps[1] != 3 {
		t.Fatalf("AffiliationGaps = %v, want [1, 3]", gaps)
	}
}
