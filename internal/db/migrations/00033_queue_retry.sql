-- +goose Up
-- A download that failed on a dead swarm is tried again later rather than failed for good: how many
-- times so far, and not before when (unix millis).
ALTER TABLE download_queue ADD COLUMN attempts INTEGER NOT NULL DEFAULT 0;
ALTER TABLE download_queue ADD COLUMN retry_at INTEGER;

-- +goose Down
ALTER TABLE download_queue DROP COLUMN retry_at;
ALTER TABLE download_queue DROP COLUMN attempts;
