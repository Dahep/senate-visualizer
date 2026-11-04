package analytics

// Vote is the ONE vocabulary of the stored individual_votes vote values
// (DESIGN.md gap 4): the closed enum the ingest boundary normalizes to, the
// schema CHECK mirrors, and every counter, bucket and label decodes
// through. It lives next to VCount so the analytics package owns what is
// cast; the camara boundary aliases this exact type (camara carries no
// parallel spelling).
type Vote string

const (
	VoteYes       Vote = "yes"
	VoteNo        Vote = "no"
	VoteAbstain   Vote = "abstain"
	VoteAbsent    Vote = "absent"
	VoteDispensed Vote = "dispensed"
	VotePaired    Vote = "paired"
	VoteOther     Vote = "other"
)

func (v Vote) String() string { return string(v) }

// Cast reports whether a stored vote is a positional vote. True exactly for
// the values the cast_votes SQL view (migration 004) lets into the Rice
// aggregates — yes/no/abstain; absent, dispensed, paired and other are not
// positions. This is the one Go spelling of the rule the view names once in
// SQL.
func (v Vote) Cast() bool {
	return v == VoteYes || v == VoteNo || v == VoteAbstain
}
