-- +goose Up
-- Episodes were keyed by TheTVDB's number inside its season, which files a
-- later cour past one (Bleach TYBW part 4 is 41-50 there) while the entry
-- itself, the list trackers, the airing schedule and most release groups count
-- it 1-10. The same episode ended up under two keys. Every such show is re-keyed
-- by its own numbering; the TVDB number stays as an alias for matching.
ALTER TABLE episode ADD COLUMN tvdb_number INTEGER;

CREATE TABLE _renumber AS
    SELECT anime_id, min(number) - 1 AS off
    FROM episode
    WHERE is_special = 0 AND number IS NOT NULL
    GROUP BY anime_id
    HAVING min(number) > 1;

UPDATE episode SET tvdb_number = number
WHERE anime_id IN (SELECT anime_id FROM _renumber) AND is_special = 0;

-- Keys are rewritten through a prefix so an overlapping range (12-22 becoming
-- 1-11) never collides with a key not yet rewritten.
UPDATE episode
SET number = number - (SELECT off FROM _renumber s WHERE s.anime_id = episode.anime_id),
    ep_key = 'n' || (number - (SELECT off FROM _renumber s WHERE s.anime_id = episode.anime_id))
WHERE anime_id IN (SELECT anime_id FROM _renumber) AND is_special = 0 AND number IS NOT NULL;
UPDATE episode SET ep_key = substr(ep_key, 2) WHERE ep_key LIKE 'n%';

-- Played under both numberings: keep the more recent.
DELETE FROM playback WHERE rowid IN (
    SELECT a.rowid FROM playback a
    JOIN _renumber s ON s.anime_id = a.anime_id
    JOIN playback b ON b.anime_id = a.anime_id
                   AND b.ep_key = CAST(CAST(a.ep_key AS INTEGER) - s.off AS TEXT)
    WHERE a.ep_key GLOB '[0-9]*' AND CAST(a.ep_key AS INTEGER) > s.off
      AND a.last_played_at <= b.last_played_at);
DELETE FROM playback WHERE rowid IN (
    SELECT b.rowid FROM playback a
    JOIN _renumber s ON s.anime_id = a.anime_id
    JOIN playback b ON b.anime_id = a.anime_id
                   AND b.ep_key = CAST(CAST(a.ep_key AS INTEGER) - s.off AS TEXT)
    WHERE a.ep_key GLOB '[0-9]*' AND CAST(a.ep_key AS INTEGER) > s.off
      AND a.last_played_at > b.last_played_at);
UPDATE playback
SET ep_key = 'n' || (CAST(ep_key AS INTEGER) - (SELECT off FROM _renumber s WHERE s.anime_id = playback.anime_id))
WHERE anime_id IN (SELECT anime_id FROM _renumber) AND ep_key GLOB '[0-9]*'
  AND CAST(ep_key AS INTEGER) > (SELECT off FROM _renumber s WHERE s.anime_id = playback.anime_id);
UPDATE playback SET ep_key = substr(ep_key, 2) WHERE ep_key LIKE 'n%';

-- Queued under both numberings is one request.
DELETE FROM download_queue WHERE rowid IN (
    SELECT a.rowid FROM download_queue a
    JOIN _renumber s ON s.anime_id = a.anime_id
    JOIN download_queue b ON b.anime_id = a.anime_id
                         AND b.ep_key = CAST(CAST(a.ep_key AS INTEGER) - s.off AS TEXT)
    WHERE a.ep_key GLOB '[0-9]*' AND CAST(a.ep_key AS INTEGER) > s.off);
UPDATE download_queue
SET ep_key = 'n' || (CAST(ep_key AS INTEGER) - (SELECT off FROM _renumber s WHERE s.anime_id = download_queue.anime_id)),
    episode = episode - (SELECT off FROM _renumber s WHERE s.anime_id = download_queue.anime_id)
WHERE anime_id IN (SELECT anime_id FROM _renumber) AND ep_key GLOB '[0-9]*'
  AND CAST(ep_key AS INTEGER) > (SELECT off FROM _renumber s WHERE s.anime_id = download_queue.anime_id);
UPDATE download_queue SET ep_key = substr(ep_key, 2) WHERE ep_key LIKE 'n%';

UPDATE torrent_file
SET ep_key = CAST(CAST(ep_key AS INTEGER) - (SELECT off FROM _renumber s WHERE s.anime_id = torrent_file.anime_id) AS TEXT)
WHERE anime_id IN (SELECT anime_id FROM _renumber) AND ep_key GLOB '[0-9]*'
  AND CAST(ep_key AS INTEGER) > (SELECT off FROM _renumber s WHERE s.anime_id = torrent_file.anime_id);

UPDATE watch_session
SET ep_key = CAST(CAST(ep_key AS INTEGER) - (SELECT off FROM _renumber s WHERE s.anime_id = watch_session.anime_id) AS TEXT)
WHERE anime_id IN (SELECT anime_id FROM _renumber) AND ep_key GLOB '[0-9]*'
  AND CAST(ep_key AS INTEGER) > (SELECT off FROM _renumber s WHERE s.anime_id = watch_session.anime_id);

UPDATE notification
SET episode = episode - (SELECT off FROM _renumber s WHERE s.anime_id = notification.anime_id)
WHERE anime_id IN (SELECT anime_id FROM _renumber)
  AND episode > (SELECT off FROM _renumber s WHERE s.anime_id = notification.anime_id);

UPDATE notified_release
SET episode = episode - (SELECT off FROM _renumber s WHERE s.anime_id = notified_release.anime_id)
WHERE anime_id IN (SELECT anime_id FROM _renumber)
  AND episode > (SELECT off FROM _renumber s WHERE s.anime_id = notified_release.anime_id);

-- Never read back; what it cached was keyed the old way.
DELETE FROM release WHERE anime_id IN (SELECT anime_id FROM _renumber);

-- A list progress past the episode count was counted the catalogue's way, and
-- marked a still-airing show complete. Corrected and pushed to the tracker.
CREATE TABLE _renumber_list AS
    SELECT e.anime_id FROM list_entry e
    JOIN _renumber s ON s.anime_id = e.anime_id
    LEFT JOIN anime a ON a.id = e.anime_id
    WHERE e.progress > s.off AND e.progress > coalesce(a.episode_count, 0);
UPDATE list_entry
SET progress = progress - (SELECT off FROM _renumber s WHERE s.anime_id = list_entry.anime_id),
    dirty = 1, local_updated_at = CAST(strftime('%s', 'now') AS INTEGER)
WHERE anime_id IN (SELECT anime_id FROM _renumber_list);
UPDATE list_entry
SET status = 'CURRENT', completed_at = NULL
WHERE anime_id IN (SELECT anime_id FROM _renumber_list) AND status = 'COMPLETED'
  AND progress < (SELECT episode_count FROM anime WHERE id = list_entry.anime_id AND episode_count > 0);

DROP TABLE _renumber_list;
DROP TABLE _renumber;

-- +goose Down
-- The re-keying is not reversed; only the alias column goes.
ALTER TABLE episode DROP COLUMN tvdb_number;
