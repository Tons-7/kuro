-- +goose Up

-- Auto-skip and auto-next become opt-in; 00005's rows were seeded defaults, not choices, so they change.
UPDATE setting SET value = 'false'
WHERE key IN ('playback.autoskip_op', 'playback.autonext');

INSERT INTO setting (key, value) VALUES ('playback.autoplay', 'false')
ON CONFLICT(key) DO NOTHING;

-- +goose Down
UPDATE setting SET value = 'true'
WHERE key IN ('playback.autoskip_op', 'playback.autonext');

DELETE FROM setting WHERE key = 'playback.autoplay';
