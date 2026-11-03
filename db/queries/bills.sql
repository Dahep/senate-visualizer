-- name: UpsertBillStub :exec
-- Writes the caller-chosen title block, nothing else. The upgrade policy -
-- recency rule, manual/enriched protection - is enforced in Go at the only
-- ingest caller (internal/ingest/sync.go applyBillTitle), which re-reads the
-- existing row inside the ingest transaction before choosing the slots this
-- statement receives. This one is a plain write.
INSERT INTO bills (id, title, title_source, title_date)
VALUES (?, ?, ?, ?)
ON CONFLICT (id) DO UPDATE SET
    title = excluded.title,
    title_source = excluded.title_source,
    title_date = excluded.title_date;

-- name: GetBill :one
SELECT * FROM bills WHERE id = ?;

-- name: ListBills :many
-- matches with a caller-supplied pattern (%term% built in Go)
SELECT * FROM bills
WHERE id LIKE ?
ORDER BY title_date DESC
LIMIT ? OFFSET ?;

-- name: CountBills :one
SELECT COUNT(*) FROM bills WHERE id LIKE ?;

-- name: GetVotationsByBill :many
SELECT * FROM votations WHERE bill_id = ? ORDER BY date DESC;
