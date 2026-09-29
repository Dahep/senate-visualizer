package analytics

import (
	"reflect"
	"testing"
)

func cofRows(votation int64, date string, yes, no, abst int64) []CohesionRow {
	r := []CohesionRow{}
	mk := func(vote string, n int64) {
		if n > 0 {
			r = append(r, CohesionRow{PartyID: 7, VotationID: votation, VoteDate: date, Vote: vote, N: n})
		}
	}
	mk("yes", yes)
	mk("no", no)
	mk("abstain", abst)
	return r
}

func TestCohesion(t *testing.T) {
	// single votation 90-5-5: max=90, cast=100 -> 0.9
	if got := Cohesion(cofRows(1, "2024-01-01", 90, 5, 5)); got.Score != 0.9 {
		t.Fatalf("Cohesion single = %v, want 0.9", got.Score)
	}
	// two votations: (90-5-5)=0.9 cast 100; (0-10-50) max=50 cast 60.
	rows := append(cofRows(1, "2024-01-01", 90, 5, 5), cofRows(2, "2024-02-01", 0, 10, 50)...)
	// aggregate Σmax/Σcast = (90+50)/160 = 0.875 (not the average of 0.9 and 0.833)
	if got := Cohesion(rows); got.Score != 140.0/160.0 {
		t.Fatalf("Cohesion aggregate = %v, want %v", got.Score, 140.0/160.0)
	}
	empty := Cohesion(nil)
	if empty.Score != 0 || empty.Meaningful() {
		t.Fatalf("Cohesion empty = %+v, want Score 0, Meaningful false", empty)
	}
	// one-voter windows are not meaningful (Rice would be trivially 100%)
	single := Cohesion(cofRows(1, "2024-01-01", 1, 0, 0))
	if single.Meaningful() {
		t.Fatalf("single-voter window reported meaningful: %+v", single)
	}
}

func TestCohesionSeries(t *testing.T) {
	rows := append(cofRows(1, "2024-01-15", 90, 5, 5), cofRows(2, "2024-01-20", 0, 10, 50)...)
	rows = append(rows, cofRows(3, "2024-02-01", 50, 25, 5)...)
	got := CohesionSeries(rows)
	want := []TrendPoint{
		{Month: "2024-01", Score: 0.875, Cast: 160},
		{Month: "2024-02", Score: 50.0 / 80.0, Cast: 80},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CohesionSeries = %+v, want %+v", got, want)
	}
}

func TestLoyalty(t *testing.T) {
	rows := []LoyaltyRow{
		// votation 1, party A (id 7): three yes, one no -> modal yes
		{RepID: 1, FirstName: "Ana", LastName: "Loy", PartyID: 7, PartyShort: "A", VotationID: 1, Vote: "yes"},
		{RepID: 2, FirstName: "Beto", LastName: "Rebel", PartyID: 7, PartyShort: "A", VotationID: 1, Vote: "no"},
		{RepID: 3, FirstName: "Ca", LastName: "Fiel", PartyID: 7, PartyShort: "A", VotationID: 1, Vote: "yes"},
		{RepID: 4, FirstName: "Di", LastName: "Fiel", PartyID: 7, PartyShort: "A", VotationID: 1, Vote: "yes"},
		{RepID: 1, FirstName: "Ana", LastName: "Loy", PartyID: 7, PartyShort: "A", VotationID: 2, Vote: "yes"},
		{RepID: 2, FirstName: "Beto", LastName: "Rebel", PartyID: 7, PartyShort: "A", VotationID: 2, Vote: "yes"},
		{RepID: 3, FirstName: "Ca", LastName: "Fiel", PartyID: 7, PartyShort: "A", VotationID: 2, Vote: "yes"},
		{RepID: 4, FirstName: "Di", LastName: "Fiel", PartyID: 7, PartyShort: "A", VotationID: 2, Vote: "no"},
	}
	got := Loyalty(rows)
	// rep1: 2/2 -> 1.0; rep2: 0/2; rep3: 1 yes yes +2 no =2/2=1.0? no: v2 modal yes (2 vs 1) -> rep2 v2 yes matches
	// rep2: v1 no (modal yes) miss, v2 yes hit -> 1/2 = 0.5
	// rep3: v1 hit, v2 hit -> 1.0; rep4: v1 hit, v2 no miss -> 0.5
	wantScores := map[int64]float64{1: 1, 2: 0.5, 3: 1, 4: 0.5}
	for _, l := range got {
		if wantScores[l.RepID] != l.Score {
			t.Fatalf("rep %d score %v, want %v", l.RepID, l.Score, wantScores[l.RepID])
		}
		if l.Name == "" || l.PartyShort != "A" {
			t.Fatalf("bad rep row %+v", l)
		}
	}
	if len(got) != 4 {
		t.Fatalf("want 4 reps, got %d", len(got))
	}
	// Rebels first: the two 0.5 entries precede the 1.0s.
	if got[0].Score >= got[len(got)-1].Score {
		t.Fatalf("not sorted ascending: %+v", got)
	}
}

func TestLoyaltyTieBreakAndName(t *testing.T) {
	rows := []LoyaltyRow{
		{RepID: 1, FirstName: "Ana", LastName: "Zulu", PartyID: 7, PartyShort: "A", VotationID: 1, Vote: "yes"},
		{RepID: 2, FirstName: "Beto", LastName: "Alic", PartyID: 7, PartyShort: "A", VotationID: 1, Vote: "yes"},
	}
	got := Loyalty(rows)
	if len(got) != 2 || got[0].Score != 1 || got[0].Name != "Alic, Beto" || got[1].Name != "Zulu, Ana" {
		t.Fatalf("unexpected %+v", got)
	}
}
