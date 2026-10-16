-- +goose Up
-- When each tracker last refused a push of the entry (unix seconds, 0 never): a refused row goes to the
-- back of the line, so a few that always fail cannot starve every newer edit.
ALTER TABLE list_entry ADD COLUMN anilist_failed_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE list_entry ADD COLUMN mal_failed_at INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE list_entry DROP COLUMN mal_failed_at;
ALTER TABLE list_entry DROP COLUMN anilist_failed_at;
