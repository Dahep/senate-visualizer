package analytics

import (
	"os"
	"strings"
	"testing"
)

// The stored vote vocabulary is exactly seven spellings; the schema CHECK
// (001) mirrors this list, the cast rule (004) carries its cast subset, and
// every count routes through it — a spelling or membership drift breaks all
// three at once, so these tests read the migrations instead of echoing the
// constants.
func TestVoteSpellingsMatchSchemaCheck(t *testing.T) {
	schema, err := os.ReadFile("../../db/migrations/001_initial_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(schema), "CREATE TABLE individual_votes")
	if start < 0 {
		t.Fatal("individual_votes table not found in schema")
	}
	checkStart := strings.Index(string(schema[start:]), "CHECK (vote IN")
	if checkStart < 0 {
		t.Fatal("vote CHECK constraint not found in schema")
	}
	// Anchor inside the CHECK literals: comments above the table also
	// quote enum values (e.g. the vote_raw escape-hatch note), and those
	// must not satisfy the membership test.
	check := string(schema[start+checkStart:])
	if !strings.Contains(check, "CHECK (vote IN ('yes', 'no', 'abstain', 'absent', 'dispensed', 'paired', 'other'))") {
		t.Errorf("vote CHECK no longer mirrors the seven-value vocabulary verbatim: %s", check[:150])
	}
	all := []Vote{VoteYes, VoteNo, VoteAbstain, VoteAbsent, VoteDispensed, VotePaired, VoteOther}
	want := []string{"yes", "no", "abstain", "absent", "dispensed", "paired", "other"}
	for i, v := range all {
		if string(v) != want[i] {
			t.Errorf("vote spelling drifted: constant %d = %q, want %q", i, v, want[i])
		}
	}
}

// The Go Cast() set and the SQL cast_votes view name the same three values.
func TestVoteCastSetMatchesCastVotesView(t *testing.T) {
	view, err := os.ReadFile("../../db/migrations/004_cast_votes.sql")
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(view), "CREATE VIEW cast_votes")
	if start < 0 {
		t.Fatal("cast_votes view not found in migration 004")
	}
	tail := string(view[start:])
	where := tail[:strings.Index(tail, ";")]
	for _, v := range []Vote{VoteYes, VoteNo, VoteAbstain} {
		if !v.Cast() {
			t.Fatalf("%q is a position vote but Cast() says no", v)
		}
		if !strings.Contains(where, "'"+string(v)+"'") {
			t.Errorf("cast_votes view is missing %q", v)
		}
	}
	for _, v := range []Vote{VoteAbsent, VoteDispensed, VotePaired, VoteOther} {
		if v.Cast() {
			t.Errorf("%q wrongly counts as cast", v)
		}
		if strings.Contains(where, "'"+string(v)+"'") {
			t.Errorf("cast_votes view wrongly includes %q", v)
		}
	}
}

func TestVoteCastMembership(t *testing.T) {
	for _, in := range []Vote{"", "torcedura", "YES"} {
		if in.Cast() {
			t.Errorf("%q wrongly counts as cast", in)
		}
	}
	if VoteYes.String() != "yes" {
		t.Fatalf("Vote.String() broken: %q", VoteYes.String())
	}
}

// VCount.Add decodes through the vocabulary: only the three position values
// land in the tally, everything else is a defensive no-op.
func TestVCountAddThroughVocabulary(t *testing.T) {
	var v VCount
	for _, c := range []struct {
		vote Vote
		n    int64
	}{
		{VoteYes, 2}, {VoteNo, 1}, {VoteAbstain, 3},
		{VoteAbsent, 5}, {VoteDispensed, 4}, {VotePaired, 6}, {VoteOther, 7}, {"torcedura", 8},
	} {
		v.Add(c.vote, c.n)
	}
	if v.Yes != 2 || v.No != 1 || v.Abstain != 3 {
		t.Fatalf("VCount after Add = %+v, want yes 2 no 1 abstain 3", v)
	}
}
