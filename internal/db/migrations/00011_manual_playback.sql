-- +goose Up

-- Migration 00005 seeded opening-skip and next-episode as on. Automatic
-- behaviour is now opt-in: skipping an opening the user wanted to watch, or
-- rolling into the next episode unasked, is worse than pressing a button.
-- These rows are rewritten rather than left alone because they were seeded
-- defaults, not choices anyone made.
UPDATE setting SET value = 'false'
WHERE key IN ('playback.autoskip_op', 'playback.autonext');

INSERT INTO setting (key, value) VALUES ('playback.autoplay', 'false')
ON CONFLICT(key) DO NOTHING;

-- +goose Down
UPDATE setting SET value = 'true'
WHERE key IN ('playback.autoskip_op', 'playback.autonext');

DELETE FROM setting WHERE key = 'playback.autoplay';
