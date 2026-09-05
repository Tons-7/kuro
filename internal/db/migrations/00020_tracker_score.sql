-- +goose Up
-- What each tracker was last told the score was, so a rating changed on the
-- site is noticed the same way progress is.
ALTER TABLE tracker_push ADD COLUMN score INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE tracker_push DROP COLUMN score;
