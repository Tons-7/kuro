-- +goose Up

-- The default player moves from mpv to the browser, which works on phones and TVs too.
UPDATE setting SET value = 'browser' WHERE key = 'playback.player' AND value = 'mpv';

-- +goose Down
UPDATE setting SET value = 'mpv' WHERE key = 'playback.player' AND value = 'browser';
