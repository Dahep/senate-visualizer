// Fixture-DB tests for ValidateAgainstDB (docs/DESIGN.md 5): rep-existence
// problems list ALL missing deputies in one pass (Apply still hard-fails on
// the first), and the coverage report flags deputies whose every vote is
// uncovered by the FILE's spans - while partial coverage and the
// '0000-00-00' sentinel stay off the error path.
package curation

import (
	"context"
	"path/filepath"
	"testing"

	"congress-visualizer/internal/database"
)

func testCurationDB(t *testing.T) (*database.OpenDB, *database.Queries) {
	t.Helper()
	odb, err := database.Open(filepath.Join(t.TempDir(), "curation.db"))
	if err != nil {
		t.Fatalf("open fixture db: %v", err)
	}
	t.Cleanup(func() { odb.Close() })
	if err := odb.Migrate("../../db/migrations"); err != nil {
		t.Fatalf("migrate fixture db: %v", err)
	}
	return odb, database.New(odb.Raw())
}

func seedValidationFixture(t *testing.T, odb *database.OpenDB) {
	t.Helper()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := odb.Raw().Exec(q, args...); err != nil {
			t.Fatalf("seed %q: %v", q[:40], err)
		}
	}
	exec(`INSERT INTO parties (short_name, name, color) VALUES ('PAAA', 'Prueba A', '#FF0000')`)
	// Ana exists with FULLY uncovered votes; Beto exists and is covered;
	// Clara has only a '0000-00-00' sentinel vote.
	exec(`INSERT INTO representatives (chamber_id, external_id, first_name, last_name, active) VALUES
		('camara', '9001', 'Ana', 'Fuentes', 1),
		('camara', '9002', 'Beto', 'Garces', 1),
		('camara', '9003', 'Clara', 'Lagos', 1)`)
	exec(`INSERT INTO votations (chamber_id, external_id, date, vote_date) VALUES
		('camara', '5001', '2018-01-10T10:00:00', '2018-01-10'),
		('camara', '5002', '2019-02-10T10:00:00', '2019-02-10'),
		('camara', '5003', '0000-00-00T00:00:00', '0000-00-00')`)
	exec(`INSERT INTO individual_votes (votation_id, representative_id, vote, vote_raw) VALUES
		(1, 1, 'yes', 'Afirmativo'), (2, 1, 'no', 'En Contra'), (3, 3, 'no', 'En Contra')`)
}

// Two missing deputies in one file: BOTH are listed (Apply would have
// stopped at the first).
func TestValidateAgainstDBListsAllMissingReps(t *testing.T) {
	odb, q := testCurationDB(t)
	file := AffiliationsFile{Affiliations: []Affiliation{
		{Chamber: "camara", ExternalID: "8888", Party: "PAAA", Start: "2018-01-01"},
		{Chamber: "camara", ExternalID: "9999", Party: "PAAA", Start: "2018-01-01"},
	}}
	problems, err := ValidateAgainstDB(context.Background(), odb.Raw(), q, file)
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 2 {
		t.Fatalf("problems = %v, want 2 (both missing reps listed)", problems)
	}
}

// Coverage: Ana has votes but no file span touches them (reported); Beto is
// fully covered (clean); Clara's only vote is the sentinel (excluded).
func TestValidateAgainstDBReportsFullyUncoveredDeputy(t *testing.T) {
	odb, q := testCurationDB(t)
	seedValidationFixture(t, odb)
	file := AffiliationsFile{Affiliations: []Affiliation{
		{Chamber: "camara", ExternalID: "9001", Party: "PAAA", Start: "1990-01-01", End: strptr("1991-01-01")},
	}}
	problems, err := ValidateAgainstDB(context.Background(), odb.Raw(), q, file)
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 1 || problems[0] != "camara/9001: 2 votes, none covered by the file" {
		t.Fatalf("problems = %v, want only Ana fully uncovered", problems)
	}

	// The file covering Beto (who has no votes at all) stays silent, and
	// fixing Ana's span clears the report entirely.
	file.Affiliations = append(file.Affiliations,
		Affiliation{Chamber: "camara", ExternalID: "9002", Party: "PAAA", Start: "2019-01-01"},
		Affiliation{Chamber: "camara", ExternalID: "9001", Party: "PAAA", Start: "2017-01-01"})
	problems, err = ValidateAgainstDB(context.Background(), odb.Raw(), q, file)
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 0 {
		t.Fatalf("problems = %v, want clean", problems)
	}
}

func strptr(s string) *string { return &s }
