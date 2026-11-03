-- name: EnqueueCandidate :exec
-- votation_candidates lifecycle (docs/DESIGN.md): the Go constant
-- ingest.candidateQueue's maxAttempts is the SINGLE source of truth - the
-- pending queries receive it as sqlc.arg(max_attempts) (pattern:
-- CAST(sqlc.arg(...) ...) in analytics.sql). Pending -> fetched on hit OR
-- miss, stamped BY IDENTITY - one row, never a batch-wide stamp; error ->
-- attempts++/last_error; attempts >= max_attempts quarantines the row
-- (excluded here, surfaced in the sync result).
INSERT INTO votation_candidates (chamber_id, external_id, source)
VALUES (?, ?, ?)
ON CONFLICT DO NOTHING;

-- name: PendingCandidates :many
SELECT * FROM votation_candidates
WHERE fetched_at IS NULL AND attempts < CAST(sqlc.arg(max_attempts) AS INTEGER)
ORDER BY discovered_at
LIMIT sqlc.arg(row_limit);

-- name: CountPendingCandidates :one
SELECT count(*) FROM votation_candidates
WHERE fetched_at IS NULL AND attempts < CAST(sqlc.arg(max_attempts) AS INTEGER);

-- name: MarkCandidateFetched :exec
UPDATE votation_candidates SET fetched_at = datetime('now')
WHERE fetched_at IS NULL AND chamber_id = ? AND external_id = ?;

-- name: FailCandidate :one
UPDATE votation_candidates SET
    attempts = attempts + 1, last_error = ?
WHERE chamber_id = ? AND external_id = ?
RETURNING attempts;
