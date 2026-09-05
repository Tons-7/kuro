-- +goose Up
-- A download someone asked for is not a cache entry: it stays until they delete
-- it and does not count against the cache budget. Nothing so far recorded who
-- asked, so existing rows stay in the cache tier.
ALTER TABLE cache_entry ADD COLUMN kept INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE cache_entry DROP COLUMN kept;
