-- +goose Up
-- Written on every corpus save and read by nothing: titles are looked up by anime_id, and search uses
-- anime_fts. The norm column stays, since dropping it would rebuild the table.
DROP TRIGGER IF EXISTS title_fts_au;
DROP TRIGGER IF EXISTS title_fts_ad;
DROP TRIGGER IF EXISTS title_fts_ai;
DROP TABLE IF EXISTS title_fts;
DROP INDEX IF EXISTS title_norm;
DROP INDEX IF EXISTS title_anime;
DROP INDEX IF EXISTS corpus_stale;

-- +goose Down
CREATE INDEX IF NOT EXISTS title_anime ON title(anime_id);
CREATE INDEX IF NOT EXISTS title_norm ON title(norm);
CREATE INDEX IF NOT EXISTS corpus_stale ON corpus_anime(titles_at);
