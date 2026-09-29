// Package analytics holds the pure Phase-2 arithmetic over query rows
// (docs/DESIGN.md gap 8): Rice-index cohesion per party, representative
// loyalty vs the party's modal vote, the 'rebels' ranking, and monthly
// cohesion series. No DB access, no IO — trivially testable.
package analytics

import (
	"sort"
)

// CohesionRow is one (party, votation, vote) count within a query window.
type CohesionRow struct {
	PartyID    int64
	VotationID int64
	VoteDate   string
	Vote       string // 'yes' | 'no' | 'abstain'
	N          int64
}

// LoyaltyRow is one (rep, votation) cast vote with its party-at-date.
type LoyaltyRow struct {
	RepID          int64
	FirstName      string
	LastName       string
	SecondLastName string
	PartyID        int64
	PartyShort     string
	VotationID     int64
	Vote           string // 'yes' | 'no' | 'abstain'
}

// VCount is the yes/no/abstain tally of one group of rows (a votation, a
// party-votation pair...).
type VCount struct{ Yes, No, Abstain int64 }

// Add folds one (vote, n) pair in; non-cast labels are ignored defensively.
func (v *VCount) Add(vote string, n int64) {
	switch vote {
	case "yes":
		v.Yes += n
	case "no":
		v.No += n
	case "abstain":
		v.Abstain += n
	}
}

// Max is the modal vote count (the majority of the three).
func (v VCount) Max() int64 {
	m := v.Yes
	if v.No > m {
		m = v.No
	}
	if v.Abstain > m {
		m = v.Abstain
	}
	return m
}

// Cast is the total of positional votes (yes+no+abstain).
func (v VCount) Cast() int64 { return v.Yes + v.No + v.Abstain }

// Rice consumes one tally-per-votation stream: total cast votes and total
// majority (max-of-three cast) votes across the stream.
func Rice(votes map[int64]VCount) (cast, majority int64) {
	for _, v := range votes {
		cast += v.Cast()
		majority += v.Max()
	}
	return cast, majority
}

// Aggregate is the vote-weighted Rice summary over a window: one score,
// the total cast votes behind it, and the number of contributing votations.
type Aggregate struct {
	Score     float64
	Cast      int64
	Votations int64
}

// Meaningful reports whether Score is informative. Rice over a group with
// a single voter is trivially 100%, so the score is only shown once the
// window averages at least two cast votes per votation.
func (a Aggregate) Meaningful() bool {
	return a.Votations > 0 && a.Cast >= 2*a.Votations
}

// Cohesion folds per-(votation, vote) count rows into the aggregate Rice
// index for ONE party: Σ_votations max(yes,no,abstain) / Σ cast. Vote-weighted,
// resistant to small-votation noise. Cast = 0 yields Score 0 (and
// Meaningful false, so callers render n/a).
func Cohesion(rows []CohesionRow) Aggregate {
	votes := map[int64]VCount{}
	for _, r := range rows {
		v := votes[r.VotationID]
		v.Add(r.Vote, r.N)
		votes[r.VotationID] = v
	}
	cast, majority := Rice(votes)
	a := Aggregate{Cast: cast, Votations: int64(len(votes))}
	if cast > 0 {
		a.Score = float64(majority) / float64(cast)
	}
	return a
}

// TrendPoint is the monthly Rice index for one party.
type TrendPoint struct {
	Month string // 'YYYY-MM'
	Score float64
	Cast  int64
}

// CohesionSeries folds cohesion rows into per-month aggregate Rice scores
// (sorted ascending by month). The aggregate Σ max / Σ cast is computed over
// the month's votations — NOT an average of per-votation values.
func CohesionSeries(rows []CohesionRow) []TrendPoint {
	months := map[string]map[int64]VCount{}
	for _, r := range rows {
		ym := r.VoteDate
		if len(ym) >= 7 {
			ym = ym[:7]
		}
		m, ok := months[ym]
		if !ok {
			m = map[int64]VCount{}
			months[ym] = m
		}
		v := m[r.VotationID]
		v.Add(r.Vote, r.N)
		m[r.VotationID] = v
	}
	out := make([]TrendPoint, 0, len(months))
	for ym, votes := range months {
		cast, majority := Rice(votes)
		score := 0.0
		if cast > 0 {
			score = float64(majority) / float64(cast)
		}
		out = append(out, TrendPoint{Month: ym, Score: score, Cast: cast})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Month < out[j].Month })
	return out
}

// RepLoyalty is one representative's loyalty summary over the window.
type RepLoyalty struct {
	RepID      int64
	Name       string
	PartyShort string
	PartyID    int64
	Votations  int64 // cast votes considered
	Majority   int64 // votes matching the party's modal vote that day
	Score      float64
}

// Loyalty computes per-representative loyalty: per (party, votation) the
// MODAL cast vote of that party, then each rep's vote compared against it.
// Returned sorted by Score ascending (rebels first), ties by most cast
// votations then name. PartyShort/PartyID come from the first row seen per
// rep: a deputy who changed affiliation mid-window aggregates their score
// across benches under the earlier party's label.
func Loyalty(rows []LoyaltyRow) []RepLoyalty {
	type pvKey struct {
		party, votation int64
	}
	modals := map[pvKey]string{}
	tallies := map[pvKey]VCount{}
	for _, r := range rows {
		k := pvKey{r.PartyID, r.VotationID}
		v := tallies[k]
		v.Add(r.Vote, 1)
		tallies[k] = v
	}
	for k, v := range tallies {
		// Modal vote: max of the three cast tallies. Exact ties (e.g. a
		// 50/50 yes/no split) resolve yes > no > abstain — documented in
		// docs/PHASE2.md caveats; only bites tiny or split benches.
		switch v.Max() {
		case v.Yes:
			modals[k] = "yes"
		case v.No:
			modals[k] = "no"
		default:
			modals[k] = "abstain"
		}
	}
	agg := map[int64]*RepLoyalty{}
	for _, r := range rows {
		l, ok := agg[r.RepID]
		if !ok {
			l = &RepLoyalty{
				RepID:      r.RepID,
				Name:       FullName(r.FirstName, r.LastName, r.SecondLastName),
				PartyShort: r.PartyShort,
				PartyID:    r.PartyID,
			}
			agg[r.RepID] = l
		}
		l.Votations++
		if modals[pvKey{r.PartyID, r.VotationID}] == r.Vote {
			l.Majority++
		}
	}
	out := make([]RepLoyalty, 0, len(agg))
	for _, l := range agg {
		l.Score = 0
		if l.Votations > 0 {
			l.Score = float64(l.Majority) / float64(l.Votations)
		}
		out = append(out, *l)
	}
	sort.Slice(out, func(i, j int) bool {
		si, sj := out[i].Score, out[j].Score
		if si != sj {
			return si < sj
		}
		if out[i].Votations != out[j].Votations {
			return out[i].Votations > out[j].Votations
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// FullName renders "Apellidos, Nombre" with the second last name folded in;
// the single display-name formatter shared by loyalty rows and handlers.
func FullName(first, last, second string) string {
	n := last
	if second != "" {
		n += " " + second
	}
	if first != "" {
		n += ", " + first
	}
	return n
}
