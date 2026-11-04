-- name: DeleteVotesByVotation :exec
DELETE FROM individual_votes WHERE votation_id = ?;

-- name: InsertVote :exec
INSERT INTO individual_votes (votation_id, representative_id, vote, vote_raw)
VALUES (?, ?, ?, ?)
ON CONFLICT (votation_id, representative_id) DO UPDATE SET
    vote = excluded.vote, vote_raw = excluded.vote_raw;

-- name: UpsertPareoVote :exec
-- Override pass: Pareos membership beats an explicit `No Vota` row. Keeping
-- the existing row's vote_raw preserves the original explicit text; rows
-- created by the pareo pass itself get vote_raw = 'Pareo' from the ?3 arg.
INSERT INTO individual_votes (votation_id, representative_id, vote, vote_raw)
VALUES (?, ?, 'paired', ?)
ON CONFLICT (votation_id, representative_id) DO UPDATE SET vote = 'paired';

-- name: GetVotesByVotation :many
SELECT iv.vote, iv.vote_raw,
       r.id AS rep_id, r.external_id, r.first_name, r.last_name, r.second_last_name,
       COALESCE(p.short_name, 'sin-partido') AS party_short,
       COALESCE(p.name, 'Sin partido') AS party_name,
       COALESCE(p.color, '#808080') AS party_color,
       pad.votation_id IS NOT NULL AS affiliated
FROM individual_votes iv
JOIN representatives r ON r.id = iv.representative_id
LEFT JOIN rep_party_at_date pad
       ON pad.votation_id = iv.votation_id
      AND pad.representative_id = iv.representative_id
LEFT JOIN parties p ON p.id = pad.party_id
WHERE iv.votation_id = ?
ORDER BY party_name, r.last_name;

-- name: GetRepresentativeVotingHistory :many
SELECT iv.*, v.date, v.subject, v.result, v.bill_id
FROM individual_votes iv
JOIN votations v ON v.id = iv.votation_id
WHERE iv.representative_id = ?
ORDER BY v.date DESC LIMIT ? OFFSET ?;

-- name: CountRepresentativeVotes :one
SELECT COUNT(*) FROM individual_votes WHERE representative_id = ?;

-- name: GetVoteCounts :many
-- Audit totals cross-check: count per normalized value.
SELECT vote, COUNT(*) AS n FROM individual_votes
WHERE votation_id = ? GROUP BY vote;
