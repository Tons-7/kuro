-- +goose Up
-- Each tracker's last-told score, so a rating changed on the site is noticed like progress.
ALTER TABLE tracker_push ADD COLUMN score INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE tracker_push DROP COLUMN score;
