-- +goose Up
ALTER TABLE playback ADD COLUMN dismissed INTEGER NOT NULL DEFAULT 0;

CREATE INDEX playback_history ON playback(anime_id, last_played_at DESC);

-- The browser player wrote ep_key as "e5" where everything else uses "5", so
-- its rows joined to nothing. Fold them onto the real key, newer of a pair wins.
DELETE FROM playback WHERE rowid IN (
    SELECT a.rowid FROM playback a
    JOIN playback b ON b.anime_id = a.anime_id AND b.ep_key = substr(a.ep_key, 2)
    WHERE a.ep_key LIKE 'e%' AND a.last_played_at <= b.last_played_at
);
DELETE FROM playback WHERE rowid IN (
    SELECT b.rowid FROM playback a
    JOIN playback b ON b.anime_id = a.anime_id AND b.ep_key = substr(a.ep_key, 2)
    WHERE a.ep_key LIKE 'e%' AND a.last_played_at > b.last_played_at
);
UPDATE playback SET ep_key = substr(ep_key, 2) WHERE ep_key LIKE 'e%';

DELETE FROM setting WHERE key = 'cache.prefetch_full';
INSERT INTO setting (key, value) VALUES ('cache.prefetch_next', 'false')
ON CONFLICT(key) DO NOTHING;

-- +goose Down
DELETE FROM setting WHERE key = 'cache.prefetch_next';
DROP INDEX IF EXISTS playback_history;
ALTER TABLE playback DROP COLUMN dismissed;
