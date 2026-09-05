-- +goose Up

-- Every known name for every anime, not just the ones on the user's list.
-- Matching a release to a show is done against this locally, because AniList's
-- own search ranks worse as the query gets more specific.
CREATE TABLE title (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    anime_id INTEGER NOT NULL,
    text     TEXT    NOT NULL,
    norm     TEXT    NOT NULL,
    kind     TEXT    NOT NULL,
    lang     TEXT,
    UNIQUE (anime_id, text)
) STRICT;

CREATE INDEX title_anime ON title(anime_id);
CREATE INDEX title_norm  ON title(norm);

CREATE VIRTUAL TABLE title_fts USING fts5(
    norm,
    content = 'title',
    content_rowid = 'id',
    tokenize = "unicode61 remove_diacritics 2",
    prefix = '2 3'
);

-- +goose StatementBegin
CREATE TRIGGER title_fts_ai AFTER INSERT ON title BEGIN
    INSERT INTO title_fts(rowid, norm) VALUES (new.id, new.norm);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER title_fts_ad AFTER DELETE ON title BEGIN
    INSERT INTO title_fts(title_fts, rowid, norm) VALUES ('delete', old.id, old.norm);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER title_fts_au AFTER UPDATE ON title BEGIN
    INSERT INTO title_fts(title_fts, rowid, norm) VALUES ('delete', old.id, old.norm);
    INSERT INTO title_fts(rowid, norm) VALUES (new.id, new.norm);
END;
-- +goose StatementEnd

-- Minimal record for anime that are only in the corpus, so a match can be
-- reported before the full AniList entry has ever been fetched.
CREATE TABLE corpus_anime (
    anime_id   INTEGER PRIMARY KEY,
    mal_id     INTEGER,
    anidb_id   INTEGER,
    kind       TEXT,
    episodes   INTEGER,
    year       INTEGER,
    season     TEXT,
    titles_at  INTEGER NOT NULL DEFAULT 0
) STRICT;

CREATE INDEX corpus_year ON corpus_anime(year);
CREATE INDEX corpus_stale ON corpus_anime(titles_at);

-- AniList ids that no longer resolve, so lookups are not retried forever.
CREATE TABLE dead_anime (
    anime_id INTEGER PRIMARY KEY
) STRICT;

CREATE TABLE corpus_source (
    name         TEXT PRIMARY KEY,
    refreshed_at INTEGER NOT NULL,
    etag         TEXT,
    records      INTEGER NOT NULL DEFAULT 0
) STRICT;

-- +goose Down
DROP TABLE IF EXISTS corpus_source;
DROP TABLE IF EXISTS dead_anime;
DROP TABLE IF EXISTS corpus_anime;
DROP TRIGGER IF EXISTS title_fts_au;
DROP TRIGGER IF EXISTS title_fts_ad;
DROP TRIGGER IF EXISTS title_fts_ai;
DROP TABLE IF EXISTS title_fts;
DROP TABLE IF EXISTS title;
