-- +goose Up

-- Seconds actually played, as opposed to the position reached. A seek to the
-- end used to count as watching the episode; now both have to agree. Seeded
-- from the position so an episode half watched before the upgrade still
-- completes.
ALTER TABLE playback ADD COLUMN played_s REAL NOT NULL DEFAULT 0;
UPDATE playback SET played_s = min(position_s, coalesce(duration_s, position_s));

-- 90% is where the ending starts on a 24-minute episode; 85% fired before it.
-- Only the seeded default moves, as with the cache budget in 00016.
UPDATE setting SET value = '0.9' WHERE key = 'sync.progress_at' AND value = '0.85';

-- "Finished" is now the position, not the watched flag. An episode already
-- watched but stopped in the old 85-90% band would otherwise reappear as in
-- progress, so pull its position up to the new threshold.
UPDATE playback SET position_s = duration_s * 0.9
    WHERE watched = 1 AND coalesce(duration_s, 0) > 0 AND position_s < duration_s * 0.9;

-- +goose Down
UPDATE setting SET value = '0.85' WHERE key = 'sync.progress_at' AND value = '0.9';
ALTER TABLE playback DROP COLUMN played_s;
