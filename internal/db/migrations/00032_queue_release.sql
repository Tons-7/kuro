-- +goose Up
-- A release chosen by hand for a queued episode, so the worker downloads that one instead of searching.
ALTER TABLE download_queue ADD COLUMN info_hash TEXT;
ALTER TABLE download_queue ADD COLUMN release_name TEXT;

-- +goose Down
ALTER TABLE download_queue DROP COLUMN release_name;
ALTER TABLE download_queue DROP COLUMN info_hash;
