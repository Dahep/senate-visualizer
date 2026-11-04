// Regression for the ONE cast-vote spelling (migration 004's cast_votes
// view). The golden suite (party_at_date_test.go) pins the cast-only query
// outputs on an all-cast fixture; THIS suite pins the rule itself on
// non-cast rows: paired, dispensed and absent votes are stored and stay
// display-visible (GetVotesByVotation), but never join a Rice aggregate
// (GetPartyVoteCounts, GetCohesionRows), a loyalty modal (GetLoyaltyRows),
// or a pairwise joint count (GetPairAgreementRows).
//
// Fixture shape on top of the shared party-at-date seed: votation V6 sits
// inside every affiliated rep's membership window and carries one cast vote
// (R1 yes), a fourth affiliated rep casting yes, and two non-cast votes
// from AFFILIATED reps (R2 dispensed, R3 paired) — the trap the four
// hand-written IN-lists used to catch; a stray absent row lands on R3/V2.
package database

import (
	"context"
	"fmt"
	"testing"
)

func TestCastVotesViewExcludesNonCast(t *testing.T) {
	q, db := testRuleDB(t)
	seedPartyAtDateFixture(t, db)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatalf("seed %q: %v", query[:40], err)
		}
	}
	exec(`INSERT INTO representatives (chamber_id, external_id, first_name, last_name, active) VALUES
		('camara', '9004', 'Diana', 'Rojas', 1)`)
	exec(`INSERT INTO representative_parties (representative_id, party_id, start_date, end_date) VALUES
		(4, 1, '2019-05-31', NULL)`)
	exec(`INSERT INTO votations (chamber_id, external_id, date, vote_date) VALUES
		('camara', '5006', '2019-06-01T10:00:00', '2019-06-01')`)
	exec(`INSERT INTO individual_votes (votation_id, representative_id, vote, vote_raw) VALUES
		(6, 1, 'yes', 'Afirmativo'),
		(6, 2, 'dispensed', 'Dispensado'),
		(6, 3, 'paired', 'Pareo'),
		(6, 4, 'yes', 'Afirmativo'),
		(2, 3, 'absent', 'No Vota')`)
	ctx := context.Background()
	castOnly := func(vote string) bool {
		return vote == "yes" || vote == "no" || vote == "abstain"
	}

	// Non-cast rows stay exactly where display needs them.
	votes6, err := q.GetVotesByVotation(ctx, 6)
	if err != nil {
		t.Fatal(err)
	}
	if len(votes6) != 4 {
		t.Fatalf("V6 rows = %d, want 4 (non-cast votes must remain stored)", len(votes6))
	}
	for _, v := range votes6 {
		if !castOnly(v.Vote) && v.VoteRaw == "" {
			t.Fatalf("V6 non-cast row lost its raw text: %+v", v)
		}
	}

	// The Rice-count queries only ever see cast values, even the
	// affiliated R2 dispensed vote on the same votation. R1 and R4 both
	// cast yes -> one grouped row of n=2.
	pc, err := q.GetPartyVoteCounts(ctx, 6)
	if err != nil {
		t.Fatal(err)
	}
	if len(pc) != 1 || pc[0].PartyID != 1 || pc[0].Vote != "yes" || pc[0].N != 2 {
		t.Fatalf("GetPartyVoteCounts V6 = %+v, want one PAAA row yes n=2", pc)
	}

	rows, err := q.GetCohesionRows(ctx, GetCohesionRowsParams{Chamber: "camara", PartyID: 0})
	if err != nil {
		t.Fatal(err)
	}
	nV6 := 0
	for _, r := range rows {
		if !castOnly(r.Vote) {
			t.Fatalf("GetCohesionRows leaked non-cast vote %q: %+v", r.Vote, r)
		}
		if r.VotationID == 6 {
			nV6++
			if r.PartyID != 1 || r.Vote != "yes" || r.N != 2 {
				t.Fatalf("V6 cohesion row = %+v, want party 1 yes n=2", r)
			}
		}
	}
	if nV6 != 1 {
		t.Fatalf("V6 contributes %d cohesion rows, want 1", nV6)
	}

	loyal, err := q.GetLoyaltyRows(ctx, GetLoyaltyRowsParams{Chamber: "camara"})
	if err != nil {
		t.Fatal(err)
	}
	nV6 = 0
	for _, r := range loyal {
		if !castOnly(r.Vote) {
			t.Fatalf("GetLoyaltyRows leaked non-cast vote %q: %+v", r.Vote, r)
		}
		if r.VotationID == 6 {
			nV6++
			if r.RepID != 1 && r.RepID != 4 {
				t.Fatalf("V6 loyalty row = %+v, want rep 1 and 4 yes only", r)
			}
		}
	}
	if nV6 != 2 {
		t.Fatalf("V6 contributes %d loyalty rows, want 2", nV6)
	}

	// Pair joints share CAST votations only: R4 is a fresh fourth member
	// casting yes on V6, so (1,4) exists with same > 0 through the
	// REAL-cast column; (1,2) must NOT gain V6 (R2 dispensed); (1,3) and
	// (2,3) must not gain V6 (R3 paired) nor V2 (R3 absent).
	pairs, err := q.GetPairAgreementRows(ctx, GetPairAgreementRowsParams{Chamber: "camara", PartyID: 0})
	if err != nil {
		t.Fatal(err)
	}
	type pairAgg struct{ joint, same float64 }
	want := map[string]pairAgg{
		"1|2": {joint: 3}, "1|3": {joint: 1}, "2|3": {joint: 2}, "1|4": {joint: 1, same: 1},
	}
	got := map[string]pairAgg{}
	for _, p := range pairs {
		got[fmt.Sprintf("%d|%d", p.AID, p.BID)] = pairAgg{joint: float64(p.Joint), same: p.Same}
	}
	if len(got) != len(want) {
		t.Fatalf("pair rows = %v, want exactly %v", got, want)
	}
	for k, w := range want {
		if got[k] != w {
			t.Fatalf("pair %s = %+v, want %+v", k, got[k], w)
		}
	}
}
