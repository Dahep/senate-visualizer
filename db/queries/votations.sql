-- name: GetVotation :one
SELECT * FROM votations WHERE id = ?;

-- name: GetVotationByExternalID :one
SELECT * FROM votations WHERE chamber_id = ? AND external_id = ?;

-- name: ListVotations :many
SELECT v.*, b.title AS bill_title
FROM votations v
LEFT JOIN bills b ON b.id = v.bill_id
WHERE (? = '' OR v.chamber_id = ?)
  AND (? = '' OR v.result = ?)
  AND (? = '' OR v.bill_id = ?)
ORDER BY v.date DESC
LIMIT ? OFFSET ?;

-- name: CountVotations :one
SELECT COUNT(*) FROM votations
WHERE (? = '' OR chamber_id = ?)
  AND (? = '' OR result = ?)
  AND (? = '' OR bill_id = ?);

-- name: UpsertVotation :one
INSERT INTO votations (
    chamber_id, external_id, session_id, bill_id, date, vote_date,
    subject, vote_type, result, quorum_type, legislative_step,
    total_yes, total_no, total_abstain, total_dispensed
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (chamber_id, external_id) DO UPDATE SET
    session_id = excluded.session_id,
    bill_id = excluded.bill_id,
    date = excluded.date,
    vote_date = excluded.vote_date,
    subject = excluded.subject,
    vote_type = excluded.vote_type,
    result = excluded.result,
    quorum_type = excluded.quorum_type,
    legislative_step = excluded.legislative_step,
    total_yes = excluded.total_yes,
    total_no = excluded.total_no,
    total_abstain = excluded.total_abstain,
    total_dispensed = excluded.total_dispensed
RETURNING id;
