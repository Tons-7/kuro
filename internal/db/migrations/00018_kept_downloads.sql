-- +goose Up
-- A requested download stays until deleted and is outside the cache budget; existing rows stay cache.
ALTER TABLE cache_entry ADD COLUMN kept INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE cache_entry DROP COLUMN kept;
