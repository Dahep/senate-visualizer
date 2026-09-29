-- name: UpsertRepresentative :one
-- DO NOTHING + RETURNING returns the existing row id on conflict (idiomatic
-- SQLite reconcile used by stub creation and enrichment alike).
INSERT INTO representatives (chamber_id, external_id, first_name, last_name, second_last_name)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (chamber_id, external_id) DO UPDATE SET
    first_name = COALESCE(NULLIF(excluded.first_name, ''), representatives.first_name),
    last_name = COALESCE(NULLIF(excluded.last_name, ''), representatives.last_name),
    second_last_name = COALESCE(NULLIF(excluded.second_last_name, ''), representatives.second_last_name)
RETURNING id;

-- name: GetRepresentativeInternalID :one
SELECT id FROM representatives WHERE chamber_id = ? AND external_id = ?;

-- name: GetRepresentative :one
SELECT r.*, COALESCE(p.short_name, 'sin-partido') AS party_short, COALESCE(p.name, 'Sin partido') AS party_name
FROM representatives r
LEFT JOIN representative_parties rp ON rp.representative_id = r.id AND rp.end_date IS NULL
LEFT JOIN parties p ON p.id = rp.party_id
WHERE r.id = ?;

-- name: GetRepresentativeNames :many
-- Display names for an explicit ID set (alignment pairs fetch names for the
-- reps actually shown instead of scanning the whole table).
SELECT id, first_name, last_name, second_last_name
FROM representatives
WHERE id IN (sqlc.slice('rep_ids'));

-- name: ListRepresentatives :many
SELECT r.*, p.short_name AS party_short, p.name AS party_name
FROM representatives r
LEFT JOIN representative_parties rp ON rp.representative_id = r.id AND rp.end_date IS NULL
LEFT JOIN parties p ON p.id = rp.party_id
WHERE (? = '' OR r.chamber_id = ?)
ORDER BY r.active DESC, r.last_name
LIMIT ? OFFSET ?;

-- name: CountRepresentatives :one
SELECT COUNT(*) FROM representatives
WHERE (? = '' OR chamber_id = ?);

-- name: EnrichRepresentative :exec
UPDATE representatives SET
    gender = COALESCE(?, gender),
    birth_date = COALESCE(?, birth_date)
WHERE chamber_id = ? AND external_id = ?;

-- name: SetRepresentativesInactive :exec
-- Called by SyncDeputies before granting active via the vigentes set.
UPDATE representatives SET active = 0 WHERE chamber_id = ?;

-- name: SetRepresentativeActive :exec
UPDATE representatives SET active = 1 WHERE chamber_id = ? AND external_id = ?;

-- name: UpsertRepresentativeFull :one
-- Used by BOTH the vote-row stub path and SyncDeputies enrichment; the
-- enrich fields (gender/birth_date) are COALESCE-time-nullable.
INSERT INTO representatives (chamber_id, external_id, first_name, last_name, second_last_name, gender, birth_date)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (chamber_id, external_id) DO UPDATE SET
    first_name = COALESCE(NULLIF(excluded.first_name, ''), representatives.first_name),
    last_name = COALESCE(NULLIF(excluded.last_name, ''), representatives.last_name),
    second_last_name = COALESCE(NULLIF(excluded.second_last_name, ''), representatives.second_last_name),
    gender = COALESCE(excluded.gender, representatives.gender),
    birth_date = COALESCE(excluded.birth_date, representatives.birth_date)
RETURNING id;
