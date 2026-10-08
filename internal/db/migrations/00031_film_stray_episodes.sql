-- +goose Up

-- A one-part film took the numbers in release names ("Movies 1 - 8") as episodes of its own.
DELETE FROM episode
WHERE number > 1
  AND title_en IS NULL AND title_ja IS NULL AND overview IS NULL AND air_date IS NULL
  AND anime_id IN (SELECT id FROM anime WHERE format = 'MOVIE' AND episode_count = 1)
  AND NOT EXISTS (SELECT 1 FROM playback p WHERE p.anime_id = episode.anime_id AND p.ep_key = episode.ep_key);

-- +goose Down
