-- +goose Up

-- Migration 00005 seeded mpv as the player, from before the browser player
-- existed. mpv cannot be used from a phone or a TV, which is most of why
-- anyone runs this on the network, so the default moves to the one that works
-- everywhere. mpv stays available globally or per show.
UPDATE setting SET value = 'browser' WHERE key = 'playback.player' AND value = 'mpv';

-- +goose Down
UPDATE setting SET value = 'mpv' WHERE key = 'playback.player' AND value = 'browser';
