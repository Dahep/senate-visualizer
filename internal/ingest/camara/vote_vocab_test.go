package camara

import (
	"testing"

	"congress-visualizer/internal/analytics"
)

// The camara boundary names are aliases into the single vote vocabulary
// owned by internal/analytics — never a parallel spelling of the closed set.
func TestVoteAliasesAreTheSharedVocabulary(t *testing.T) {
	for _, pair := range []struct {
		mine analytics.Vote
		own  analytics.Vote
	}{
		{VoteYes, analytics.VoteYes},
		{VoteNo, analytics.VoteNo},
		{VoteAbstain, analytics.VoteAbstain},
		{VoteAbsent, analytics.VoteAbsent},
		{VoteDispensed, analytics.VoteDispensed},
		{VotePaired, analytics.VotePaired},
		{VoteOther, analytics.VoteOther},
	} {
		if pair.mine != pair.own {
			t.Fatalf("camara vote constant %q diverged from the vocabulary %q", pair.mine, pair.own)
		}
	}
}

// Every raw <Opcion> text observed in the fixtures and the live API routes
// into the closed vocabulary, and ONLY the three positional values (the
// cast_votes view subset) count as cast — pinning mapping.go's mapping as
// the source of truth, not invented behavior.
func TestNormalizeVoteOptionCastSplit(t *testing.T) {
	cases := []struct {
		in   string
		want Vote
		cast bool
	}{
		{"Afirmativo", VoteYes, true},
		{"EN CONTRA", VoteNo, true},
		{"Abstencion", VoteAbstain, true},
		{"abstención", VoteAbstain, true},
		{"No Vota", VoteAbsent, false},
		{"Dispensado", VoteDispensed, false},
		{"Dispensada", VoteDispensed, false},
		{"Pareo", VotePaired, false},
		{"SEÑOR DIPUTADO", VoteOther, false},
		{"", VoteOther, false},
	}
	for _, c := range cases {
		got := NormalizeVoteOption(c.in)
		if got != c.want {
			t.Errorf("%q -> %q, want %q", c.in, got, c.want)
		}
		if got.Cast() != c.cast {
			t.Errorf("%q -> %q cast=%v, want %v", c.in, got, got.Cast(), c.cast)
		}
	}
}
