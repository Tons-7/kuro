-- +goose Up
-- A release picked by hand is exempt from the title check that guards
-- against releases recorded for the wrong show.
ALTER TABLE torrent ADD COLUMN manual INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE torrent DROP COLUMN manual;
