-- +goose Up
-- The library's default sort, which otherwise builds a temp b-tree per page.
CREATE INDEX IF NOT EXISTS list_entry_updated ON list_entry(local_updated_at);

-- An import resolves every entry by MAL id, and titles never imported into
-- anime fall through to the corpus, which had no index for it.
CREATE INDEX IF NOT EXISTS corpus_mal ON corpus_anime(mal_id) WHERE mal_id IS NOT NULL;

-- Checked once per release the watcher inspects, on a table that only grows.
CREATE INDEX IF NOT EXISTS notified_episode ON notified_release(anime_id, episode);

-- +goose Down
DROP INDEX IF EXISTS list_entry_updated;
DROP INDEX IF EXISTS corpus_mal;
DROP INDEX IF EXISTS notified_episode;
