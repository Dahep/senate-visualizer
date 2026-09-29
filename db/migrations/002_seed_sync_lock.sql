-- +goose Up
-- Lease row exists so the single-writer CAS can always UPDATE it; value
-- '' means free. (GetSyncValue reads return no rows otherwise.)
INSERT INTO sync_metadata (key, value) VALUES ('camara.sync_lock', '');

-- +goose Down
DELETE FROM sync_metadata WHERE key = 'camara.sync_lock';
