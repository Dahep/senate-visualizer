// The operator-visible result wiring (DESIGN.md 5/graft C): afterSweep fills
// the affiliation coverage-gap count the sync result reports - surfaced,
// never an ingest failure.
package ingest

import (
	"context"
	"testing"
)

func TestAfterSweepSurfacesAffiliationGaps(t *testing.T) {
	s, db := newTestSyncer(t)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("seed %q: %v", q[:40], err)
		}
	}
	exec(`INSERT INTO parties (short_name, name, color) VALUES ('PAAA', 'Prueba A', '#FF0000')`)
	exec(`INSERT INTO representatives (chamber_id, external_id, first_name, last_name, active) VALUES
		('camara', '9001', 'Ana', 'Fuentes', 1),
		('camara', '9002', 'Beto', 'Garces', 1),
		('camara', '9003', 'Clara', 'Lagos', 1)`)
	// Ana: one span whose gap a later vote falls into; Beto fully covered;
	// Clara never has a row.
	exec(`INSERT INTO representative_parties (representative_id, party_id, start_date, end_date) VALUES
		(1, 1, '2018-01-01', '2019-06-30')`)
	exec(`INSERT INTO representative_parties (representative_id, party_id, start_date) VALUES
		(2, 1, '2019-01-01')`)
	exec(`INSERT INTO votations (chamber_id, external_id, date, vote_date) VALUES
		('camara', '5001', '2019-03-10T10:00:00', '2019-03-10'),
		('camara', '5002', '2019-09-10T10:00:00', '2019-09-10')`)
	exec(`INSERT INTO individual_votes (votation_id, representative_id, vote, vote_raw) VALUES
		(1, 1, 'yes', 'Afirmativo'), (2, 1, 'yes', 'Afirmativo'),
		(1, 2, 'yes', 'Afirmativo'), (2, 2, 'yes', 'Afirmativo'),
		(1, 3, 'abstain', 'Abstencion')`)

	var res ScanResult
	if err := s.afterSweep(context.Background(), &res); err != nil {
		t.Fatalf("afterSweep: %v", err)
	}
	if res.AffiliationGaps != 2 {
		t.Fatalf("AffiliationGaps = %d, want 2 (Ana's gap vote + Clara)", res.AffiliationGaps)
	}
	if res.MinVoteDate != "2019-03-10" {
		t.Fatalf("MinVoteDate = %q, want 2019-03-10", res.MinVoteDate)
	}
}
