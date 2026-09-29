-- name: EnqueueCandidate :exec
INSERT INTO votation_candidates (chamber_id, external_id, source)
VALUES (?, ?, ?)
ON CONFLICT DO NOTHING;

-- name: PendingCandidates :many
SELECT * FROM votation_candidates
WHERE fetched_at IS NULL AND attempts < 5
ORDER BY discovered_at
LIMIT ?;

-- name: MarkCandidateFetched :exec
UPDATE votation_candidates SET fetched_at = datetime('now') WHERE fetched_at IS NULL;

-- name: FailCandidate :exec
UPDATE votation_candidates SET
    attempts = attempts + 1, last_error = ?
WHERE chamber_id = ? AND external_id = ?;
