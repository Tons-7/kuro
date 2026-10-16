-- +goose Up

-- Seconds actually played, so a seek to the end is not watching; seeded from the position.
ALTER TABLE playback ADD COLUMN played_s REAL NOT NULL DEFAULT 0;
UPDATE playback SET played_s = min(position_s, coalesce(duration_s, position_s));

-- 90% is where a 24-minute episode's ending starts; only the seeded default moves.
UPDATE setting SET value = '0.9' WHERE key = 'sync.progress_at' AND value = '0.85';

-- Watched episodes stopped at 85-90% move up to 90%, or the new threshold would call them unfinished.
UPDATE playback SET position_s = duration_s * 0.9
    WHERE watched = 1 AND coalesce(duration_s, 0) > 0 AND position_s < duration_s * 0.9;

-- +goose Down
UPDATE setting SET value = '0.85' WHERE key = 'sync.progress_at' AND value = '0.9';
ALTER TABLE playback DROP COLUMN played_s;
