// ValidateAgainstDB is the DB half of the seed boundary (docs/DESIGN.md 5,
// call site 3): it appends the problems the pure Validate cannot see.
package curation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"congress-visualizer/internal/database"
)

// ValidateAgainstDB checks the file against the database, per DESIGN.md 5:
//
//   - every affiliation's (chamber, external_id) must resolve to a deputy
//     row. Apply hard-fails on the FIRST missing rep; validation lists ALL
//     of them in one pass (the deputies sync must run first);
//
//   - a deputy WITH votes but NO file span covering ANY of them is reported
//     ("deputies with votes but no covering membership"): a deputy whose
//     every vote is uncovered is the name-alignment failure mode the
//     SERVEL/BCN drafters can produce, not a true independent. Partial gaps
//     are the sync result's AffiliationGaps trend (DESIGN.md 5), never hard
//     validation errors - one missed span row would otherwise block the
//     whole seed.
//
// The coverage test mirrors the rep_party_at_date rule (migration 003)
// locally: the FILE's spans are not DB rows yet, so the view cannot judge
// them - but the predicate is the same inclusive both-bounds rule, and the
// '0000-00-00' sentinel is excluded exactly like AffiliationGaps does.
func ValidateAgainstDB(ctx context.Context, db *sql.DB, q *database.Queries, f AffiliationsFile) ([]string, error) {
	problems := []string{}

	// File spans, keyed by the internal rep id the coverage test joins on.
	if _, err := db.ExecContext(ctx, `CREATE TEMP TABLE file_spans (
		representative_id INTEGER NOT NULL,
		start_date        TEXT NOT NULL,
		end_date          TEXT)`); err != nil {
		return nil, fmt.Errorf("temp file spans: %w", err)
	}
	defer func() { _, _ = db.ExecContext(context.Background(), `DROP TABLE file_spans`) }()

	for _, a := range f.Affiliations {
		rid, err := q.GetRepresentativeInternalID(ctx, database.GetRepresentativeInternalIDParams{
			ChamberID: a.Chamber, ExternalID: a.ExternalID,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				problems = append(problems, fmt.Sprintf("%s/%s: affiliation rep missing (run deputies sync first)", a.Chamber, a.ExternalID))
				continue
			}
			return nil, err
		}
		end := sql.NullString{}
		if a.End != nil {
			end = sql.NullString{String: *a.End, Valid: true}
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO file_spans
			(representative_id, start_date, end_date) VALUES (?, ?, ?)`, rid, a.Start, end); err != nil {
			return nil, fmt.Errorf("temp file span %s/%s: %w", a.Chamber, a.ExternalID, err)
		}
	}

	// Zero-coverage deputies: some votes, none covered by the file.
	rows, err := db.QueryContext(ctx, `
		SELECT r.chamber_id, r.external_id, COUNT(*)
		FROM individual_votes iv
		JOIN votations v ON v.id = iv.votation_id
		JOIN representatives r ON r.id = iv.representative_id
		WHERE v.vote_date <> '0000-00-00'
		GROUP BY r.id
		HAVING SUM(CASE WHEN EXISTS (
			SELECT 1 FROM file_spans s
			WHERE s.representative_id = r.id
			  AND s.start_date <= v.vote_date
			  AND (s.end_date IS NULL OR s.end_date >= v.vote_date)
		) THEN 1 ELSE 0 END) = 0`)
	if err != nil {
		return nil, fmt.Errorf("coverage scan: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var chamber, external string
		var votes int64
		if err := rows.Scan(&chamber, &external, &votes); err != nil {
			return nil, err
		}
		problems = append(problems, fmt.Sprintf("%s/%s: %d votes, none covered by the file", chamber, external, votes))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return problems, nil
}
