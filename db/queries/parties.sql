-- name: UpsertParty :exec
INSERT INTO parties (short_name, name, color) VALUES (?, ?, ?)
ON CONFLICT (short_name) DO UPDATE SET
    name = excluded.name, color = excluded.color;

-- name: ListParties :many
SELECT * FROM parties ORDER BY name;

-- name: GetPartyByShortName :one
SELECT * FROM parties WHERE short_name = ?;

-- name: GetParty :one
SELECT * FROM parties WHERE id = ?;

-- name: DeleteCuratedAffiliations :exec
DELETE FROM representative_parties WHERE source = 'curated';

-- name: InsertAffiliation :exec
INSERT INTO representative_parties (representative_id, party_id, start_date, end_date, source)
VALUES (?, ?, ?, ?, 'curated');

-- name: AffiliationGaps :many
-- Coverage-gap signal the seed workflow reports to the operator (DESIGN.md
-- 5/graft C): deputies WITH at least one vote the party-at-date rule (the
-- rep_party_at_date view) does not cover - an uncovered vote is one gap. The
-- '0000-00-00' unparseable-date sentinel is excluded: those votes render
-- unaffiliated by ingest design, and no file row can ever cover them. True
-- independents vs missing rows are the operator's decision - surfaced,
-- never an ingest failure.
SELECT DISTINCT iv.representative_id AS rid
FROM individual_votes iv
JOIN votations v ON v.id = iv.votation_id
LEFT JOIN rep_party_at_date pad
       ON pad.votation_id = iv.votation_id
      AND pad.representative_id = iv.representative_id
WHERE v.vote_date <> '0000-00-00'
  AND pad.party_id IS NULL;

-- name: PartyMembers :many
-- Current members of one party (membership with end_date IS NULL).
SELECT r.*, p.short_name AS party_short, p.name AS party_name
FROM representatives r
JOIN representative_parties rp ON rp.representative_id = r.id AND rp.end_date IS NULL
JOIN parties p ON p.id = rp.party_id
WHERE rp.party_id = ?
ORDER BY r.active DESC, r.last_name;
