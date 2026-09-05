-- +goose Up
-- Seconds actually played per calendar day, for the stats on the history page.
-- Kept apart from playback rows, which are per episode and overwritten.
CREATE TABLE watch_time (
    day     TEXT PRIMARY KEY,
    seconds REAL NOT NULL DEFAULT 0
) STRICT;

-- +goose Down
DROP TABLE watch_time;
