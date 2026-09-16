// Package curation is the parties/affiliations workflow (docs gap 5):
// JSON files in git are the single source of truth; validation is pure and
// hard; Apply is an idempotent delete-and-reinsert in one transaction.
package curation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"congress-visualizer/internal/database"
)

// PartyFile: short_name is the stable join key used by affiliations.json.
type Party struct {
	ShortName string `json:"short_name"` // 'RN', 'UDI', 'IND'
	Name      string `json:"name"`
	Color     string `json:"color"`
}

// Affiliation: date ranges per deputy; end empty = currently serving.
type Affiliation struct {
	Chamber    string  `json:"chamber"`
	ExternalID string  `json:"external_id"`
	Party      string  `json:"party"` // parties.json short_name
	Start      string  `json:"start"`
	End        *string `json:"end"`
}

type AffiliationsFile struct {
	Parties      []Party
	Affiliations []Affiliation
}

// parseJSON reads a curated file from disk.
func parseJSON(path string, v any) error {
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, v)
}

var (
	colorRE   = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
	dateRE    = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	shortRE   = regexp.MustCompile(`^[A-Z0-9][A-Za-z0-9._-]{0,31}$`)
)

// LoadAffiliations parses both curated files with their own schema checks.
func LoadAffiliations(partiesPath, affiliationsPath string) (AffiliationsFile, error) {
	var f AffiliationsFile
	if err := parseJSON(partiesPath, &f.Parties); err != nil {
		return f, fmt.Errorf("parties: %w", err)
	}
	if err := parseJSON(affiliationsPath, &f.Affiliations); err != nil {
		// Accept the annotated bot-draft shape: {"_note":..., "affiliations":[...]}
		var doc struct {
			Affiliations []Affiliation `json:"affiliations"`
		}
		if err2 := parseJSON(affiliationsPath, &doc); err2 != nil || doc.Affiliations == nil {
			return f, fmt.Errorf("affiliations: %w", err)
		}
		f.Affiliations = doc.Affiliations
	}
	return f, nil
}

// Validate is a pure function: dupes, overlaps, date order, unknown parties.
func Validate(f AffiliationsFile) (problems []string) {
	byShort := map[string]Party{}
	shortSeen := map[string]bool{}
	nameSeen := map[string]bool{}
	for _, p := range f.Parties {
		if p.ShortName == "" {
			problems = append(problems, "party: empty short_name")
		} else if !shortRE.MatchString(p.ShortName) {
			problems = append(problems, "party short_name malformed: "+p.ShortName)
		}
		if shortSeen[p.ShortName] {
			problems = append(problems, "party duplicated: "+p.ShortName)
		}
		if nameSeen[p.Name] {
			problems = append(problems, "party name duplicated: "+p.Name)
		}
		if !colorRE.MatchString(p.Color) {
			problems = append(problems, fmt.Sprintf("party %s: bad color %q", p.ShortName, p.Color))
		}
		shortSeen[p.ShortName], nameSeen[p.Name] = true, true
		byShort[p.ShortName] = p
	}

	type span struct{ start, end string }
	spans := map[string][]span{}
	for _, a := range f.Affiliations {
		key := a.Chamber + "/" + a.ExternalID
		if err := checkAffiliation(byShort, a); err != nil {
			problems = append(problems, key+": "+err.Error())
			continue
		}
		end := ""
		if a.End != nil {
			end = *a.End
		}
		spans[key] = append(spans[key], span{a.Start, end})
	}
	for key, ss := range spans {
		sort.Slice(ss, func(i, j int) bool { return ss[i].start < ss[j].start })
		for i := 1; i < len(ss); i++ {
			if ss[i].start != ss[i-1].start && (ss[i-1].end == "" || ss[i].start <= ss[i-1].end) {
				problems = append(problems, fmt.Sprintf("%s: overlapping party ranges", key))
			}
		}
	}
	return problems
}

func checkAffiliation(byShort map[string]Party, a Affiliation) error {
	if a.Chamber == "" {
		return fmt.Errorf("empty chamber")
	}
	if a.ExternalID == "" {
		return fmt.Errorf("empty external_id")
	}
	if !shortRE.MatchString(a.Party) {
		return fmt.Errorf("party slug malformed: %q", a.Party)
	}
	if _, ok := byShort[a.Party]; !ok {
		return fmt.Errorf("unknown party short_name: %q", a.Party)
	}
	if !dateRE.MatchString(a.Start) {
		return fmt.Errorf("start not YYYY-MM-DD: %q", a.Start)
	}
	if a.End != nil {
		if !dateRE.MatchString(*a.End) {
			return fmt.Errorf("end not YYYY-MM-DD: %q", *a.End)
		}
		if *a.End <= a.Start {
			return fmt.Errorf("end %q <= start %q", *a.End, a.Start)
		}
	}
	return nil
}

// Apply reconciles curations idempotently in ONE transaction:
// delete source='curated' rows, insert the file rows. File is truth.
func Apply(ctx context.Context, db *sql.DB, q *database.Queries, f AffiliationsFile) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	qt := q.WithTx(tx)
	if err := qt.DeleteCuratedAffiliations(ctx); err != nil {
		return err
	}
	for _, a := range f.Affiliations {
		rid, err := qt.GetRepresentativeInternalID(ctx, database.GetRepresentativeInternalIDParams{
			ChamberID: a.Chamber, ExternalID: a.ExternalID,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("affiliation rep missing %s/%s (run deputies sync first)", a.Chamber, a.ExternalID)
			}
			return err
		}
		party, err := qt.GetPartyByShortName(ctx, a.Party)
		if err != nil {
			return err
		}
		var end sql.NullString
		if a.End != nil {
			end = sql.NullString{String: *a.End, Valid: true}
		}
		if err := qt.InsertAffiliation(ctx, database.InsertAffiliationParams{
			RepresentativeID: rid, PartyID: party.ID,
			StartDate: a.Start, EndDate: end,
		}); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SeedParties calls the same upsert-on-short_name logic (name/color updates
// converge; short_name itself is the immutable identity — renames need a
// reviewed affiliations.json change and become NEW parties by design).
func SeedParties(ctx context.Context, q *database.Queries, f AffiliationsFile) error {
	for _, p := range f.Parties {
		if err := q.UpsertParty(ctx, database.UpsertPartyParams{
			ShortName: p.ShortName, Name: p.Name, Color: p.Color,
		}); err != nil {
			return err
		}
	}
	return nil
}

// ValidationError lists ALL problems at once (exit nonzero for the operator).
type ValidationError struct{ Problems []string }

func (e *ValidationError) Error() string {
	return fmt.Sprintf("%d affiliation problems:\n- %s",
		len(e.Problems), strings.Join(e.Problems, "\n- "))
}
