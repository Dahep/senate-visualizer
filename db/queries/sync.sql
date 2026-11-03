-- name: GetSyncValue :one
SELECT value FROM sync_metadata WHERE key = ?;

-- name: UpsertSyncValue :exec
INSERT INTO sync_metadata (key, value, updated_at) VALUES (?, ?, datetime('now'))
ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = datetime('now');

-- name: AcquireSyncLock :execrows
-- Single-writer lease CAS. Semantics (documented with the Go caller, which
-- is also where the single-writer invariant lives): the CAS succeeds when
-- the lease is unclaimed (empty), expired (any owner may steal only AFTER
-- expiry), or current-owner (renewal while the lease is still live).
UPDATE sync_metadata SET value = ?, updated_at = datetime('now')
WHERE key = 'camara.sync_lock'
  AND (SUBSTR(value, 1, INSTR(value, '|') - 1) = ?
    OR value = ''
    OR CAST(SUBSTR(value, INSTR(value, '|') + 1) AS INTEGER) < CAST(strftime('%s', 'now') AS INTEGER));

-- name: ReleaseSyncLock :exec
UPDATE sync_metadata SET value = '' WHERE key = 'camara.sync_lock';

-- name: GetMinVoteDate :one
SELECT CAST(CASE WHEN MIN(vote_date) IS NULL THEN ''
            WHEN MIN(vote_date) = '0000-00-00' THEN ''
            ELSE MIN(vote_date) END AS TEXT) AS min_vote_date
FROM votations WHERE chamber_id = 'camara';
