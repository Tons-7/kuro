-- +goose Up
-- The push/merge pair the list entries already use: what the tracker was last
-- known to hold, and whether the local value has moved since.
ALTER TABLE user_anime ADD COLUMN favourite_synced INTEGER NOT NULL DEFAULT 0;
ALTER TABLE user_anime ADD COLUMN favourite_dirty INTEGER NOT NULL DEFAULT 0;

-- Everything already favourited here predates the sync, so it has to go up.
UPDATE user_anime SET favourite_dirty = 1 WHERE favourite = 1;

-- +goose Down
ALTER TABLE user_anime DROP COLUMN favourite_synced;
ALTER TABLE user_anime DROP COLUMN favourite_dirty;
