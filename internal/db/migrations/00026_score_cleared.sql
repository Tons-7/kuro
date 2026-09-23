-- +goose Up

-- A score set back to "unrated" on purpose. Zero alone can't say so: a row
-- created locally is 0 too, and pushing that would wipe the site's score.
ALTER TABLE list_entry ADD COLUMN score_cleared INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE list_entry DROP COLUMN score_cleared;
