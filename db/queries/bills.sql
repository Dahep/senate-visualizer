-- name: UpsertBillStub :exec
-- Title upgrade policy is decided in Go (most-recent-votation Articulo wins);
-- the caller passes the chosen title/source/date. Placeholder rows are always
-- upgradeable; 'manual'/'enriched' rows must be protected in Go by re-reading
-- the existing row inside the ingest transaction when in doubt.
INSERT INTO bills (id, title, title_source, title_date)
VALUES (?, ?, ?, ?)
ON CONFLICT (id) DO UPDATE SET
    title = excluded.title,
    title_source = excluded.title_source,
    title_date = excluded.title_date;

-- name: GetBill :one
SELECT * FROM bills WHERE id = ?;

-- name: ListBills :many
-- matches with a caller-supplied pattern ('%term%' built in Go)
SELECT * FROM bills
WHERE id LIKE ?
ORDER BY title_date DESC
LIMIT ? OFFSET ?;

-- name: CountBills :one
SELECT COUNT(*) FROM bills WHERE id LIKE ?;

-- name: GetVotationsByBill :many
SELECT * FROM votations WHERE bill_id = ? ORDER BY date DESC;
