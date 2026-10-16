-- +goose Up
-- A hand-picked release skips the wrong-show title check.
ALTER TABLE torrent ADD COLUMN manual INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE torrent DROP COLUMN manual;
