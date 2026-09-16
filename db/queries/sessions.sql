-- name: UpsertSession :one
INSERT INTO sessions (chamber_id, external_id, number, date, type)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (chamber_id, external_id) DO UPDATE SET
    number = excluded.number, date = excluded.date, type = excluded.type
RETURNING id;
