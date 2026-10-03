-- +goose Up

-- Planned and never used: nothing reads or writes these.
DROP TABLE IF EXISTS watch_session;
DROP TABLE IF EXISTS release;
DROP TABLE IF EXISTS job;
DROP TABLE IF EXISTS home_widget;
DROP TABLE IF EXISTS image_cache;

-- Follow filters had no UI; per-show preferences decide quality and size.
ALTER TABLE follow DROP COLUMN quality;
ALTER TABLE follow DROP COLUMN group_filter;
ALTER TABLE follow DROP COLUMN max_bytes;
ALTER TABLE follow DROP COLUMN last_grabbed;

-- +goose Down
ALTER TABLE follow ADD COLUMN quality TEXT;
ALTER TABLE follow ADD COLUMN group_filter TEXT;
ALTER TABLE follow ADD COLUMN max_bytes INTEGER;
ALTER TABLE follow ADD COLUMN last_grabbed INTEGER;

CREATE TABLE watch_session (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    anime_id   INTEGER NOT NULL REFERENCES anime(id) ON DELETE CASCADE,
    ep_key     TEXT    NOT NULL,
    player     TEXT    NOT NULL,
    started_at INTEGER NOT NULL,
    ended_at   INTEGER,
    watched_s  REAL    NOT NULL DEFAULT 0
) STRICT;
CREATE INDEX session_time ON watch_session(started_at DESC);

CREATE TABLE release (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    anime_id    INTEGER NOT NULL REFERENCES anime(id) ON DELETE CASCADE,
    ep_key      TEXT,
    info_hash   TEXT    NOT NULL,
    title       TEXT    NOT NULL,
    size_bytes  INTEGER NOT NULL DEFAULT 0,
    seeders     INTEGER NOT NULL DEFAULT 0,
    leechers    INTEGER NOT NULL DEFAULT 0,
    group_name  TEXT,
    resolution  TEXT,
    source      TEXT,
    codec       TEXT,
    bit_depth   INTEGER,
    dual_audio  INTEGER NOT NULL DEFAULT 0,
    is_batch    INTEGER NOT NULL DEFAULT 0,
    crc32       TEXT,
    indexer     TEXT NOT NULL,
    score       REAL NOT NULL DEFAULT 0,
    seen_at     INTEGER NOT NULL,
    UNIQUE (anime_id, ep_key, info_hash)
) STRICT;
CREATE INDEX release_pick ON release(anime_id, ep_key, score DESC);

CREATE TABLE job (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    kind     TEXT    NOT NULL,
    payload  TEXT    NOT NULL DEFAULT '{}',
    state    TEXT    NOT NULL DEFAULT 'pending',
    attempts INTEGER NOT NULL DEFAULT 0,
    run_at   INTEGER NOT NULL,
    error    TEXT
) STRICT;
CREATE INDEX job_queue ON job(state, run_at);

CREATE TABLE home_widget (
    kind     TEXT PRIMARY KEY,
    position INTEGER NOT NULL,
    visible  INTEGER NOT NULL DEFAULT 1
) STRICT;

CREATE TABLE image_cache (
    url        TEXT PRIMARY KEY,
    path       TEXT NOT NULL,
    bytes      INTEGER NOT NULL,
    mime       TEXT NOT NULL,
    fetched_at INTEGER NOT NULL
) STRICT;
