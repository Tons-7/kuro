-- +goose Up

-- AniList is the identity spine: anime.id is always the AniList media id.
CREATE TABLE anime (
    id                 INTEGER PRIMARY KEY,
    mal_id             INTEGER,
    anidb_id           INTEGER,
    tvdb_id            INTEGER,
    tmdb_id            TEXT,
    kitsu_id           INTEGER,

    title_romaji       TEXT NOT NULL,
    title_english      TEXT,
    title_native       TEXT,
    synonyms           TEXT NOT NULL DEFAULT '[]',

    format             TEXT,
    status             TEXT,
    episode_count      INTEGER,
    duration           INTEGER,
    season             TEXT,
    season_year        INTEGER,
    country            TEXT,
    is_adult           INTEGER NOT NULL DEFAULT 0,

    description        TEXT,
    cover_url          TEXT,
    cover_color        TEXT,
    banner_url         TEXT,
    average_score      INTEGER,
    popularity         INTEGER,
    genres             TEXT NOT NULL DEFAULT '[]',
    studios            TEXT NOT NULL DEFAULT '[]',

    next_episode       INTEGER,
    next_airing_at     INTEGER,

    synced_at          INTEGER NOT NULL,
    metadata_synced_at INTEGER
) STRICT;

CREATE INDEX anime_mal        ON anime(mal_id) WHERE mal_id IS NOT NULL;
CREATE INDEX anime_season     ON anime(season_year, season);
CREATE INDEX anime_next_air   ON anime(next_airing_at) WHERE next_airing_at IS NOT NULL;

-- Every title variant flattened into one index so "AoT" and "Shingeki" both hit.
CREATE VIRTUAL TABLE anime_fts USING fts5(
    romaji, english, native, synonyms,
    content = 'anime',
    content_rowid = 'id',
    tokenize = "unicode61 remove_diacritics 2",
    prefix = '2 3'
);

-- +goose StatementBegin
CREATE TRIGGER anime_fts_ai AFTER INSERT ON anime BEGIN
    INSERT INTO anime_fts(rowid, romaji, english, native, synonyms)
    VALUES (new.id, new.title_romaji, new.title_english, new.title_native, new.synonyms);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER anime_fts_ad AFTER DELETE ON anime BEGIN
    INSERT INTO anime_fts(anime_fts, rowid, romaji, english, native, synonyms)
    VALUES ('delete', old.id, old.title_romaji, old.title_english, old.title_native, old.synonyms);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER anime_fts_au AFTER UPDATE ON anime BEGIN
    INSERT INTO anime_fts(anime_fts, rowid, romaji, english, native, synonyms)
    VALUES ('delete', old.id, old.title_romaji, old.title_english, old.title_native, old.synonyms);
    INSERT INTO anime_fts(rowid, romaji, english, native, synonyms)
    VALUES (new.id, new.title_romaji, new.title_english, new.title_native, new.synonyms);
END;
-- +goose StatementEnd

-- ep_key is TEXT because specials are keyed "S1".."Sn" alongside "1".."N".
CREATE TABLE episode (
    anime_id      INTEGER NOT NULL REFERENCES anime(id) ON DELETE CASCADE,
    ep_key        TEXT    NOT NULL,
    number        INTEGER,
    absolute      INTEGER,
    season_number INTEGER,
    is_special    INTEGER NOT NULL DEFAULT 0,

    title_en      TEXT,
    title_ja      TEXT,
    title_romaji  TEXT,
    overview      TEXT,
    still_url     TEXT,
    air_date      INTEGER,
    runtime       INTEGER,

    anidb_eid     INTEGER,
    tvdb_id       INTEGER,

    PRIMARY KEY (anime_id, ep_key)
) STRICT;

CREATE INDEX episode_air ON episode(air_date) WHERE air_date IS NOT NULL;

-- kind: manga-canon | anime-canon | mixed | filler | recap
-- user_kind wins when set, so a wrong upstream classification stays corrected.
CREATE TABLE filler (
    mal_id    INTEGER NOT NULL,
    number    INTEGER NOT NULL,
    kind      TEXT    NOT NULL,
    user_kind TEXT,
    source    TEXT    NOT NULL,
    PRIMARY KEY (mal_id, number)
) STRICT;

-- kind: op | ed | mixed-op | mixed-ed | recap
CREATE TABLE skip_time (
    mal_id         INTEGER NOT NULL,
    number         INTEGER NOT NULL,
    kind           TEXT    NOT NULL,
    start_s        REAL    NOT NULL,
    end_s          REAL    NOT NULL,
    episode_length REAL,
    skip_id        TEXT,
    PRIMARY KEY (mal_id, number, kind)
) STRICT;

-- Mirrors the AniList list. id is MediaList.id, which is what mutations take.
-- One row per anime: AniList returns an entry once per custom list it belongs to,
-- and those duplicate references all share this id.
CREATE TABLE list_entry (
    id                INTEGER PRIMARY KEY,
    anime_id          INTEGER NOT NULL UNIQUE REFERENCES anime(id) ON DELETE CASCADE,
    status            TEXT,
    progress          INTEGER NOT NULL DEFAULT 0,
    score             INTEGER NOT NULL DEFAULT 0,
    repeat_count      INTEGER NOT NULL DEFAULT 0,
    notes             TEXT,
    private           INTEGER NOT NULL DEFAULT 0,
    hidden            INTEGER NOT NULL DEFAULT 0,
    custom_lists      TEXT NOT NULL DEFAULT '[]',
    started_at        TEXT,
    completed_at      TEXT,

    remote_updated_at INTEGER NOT NULL DEFAULT 0,
    local_updated_at  INTEGER NOT NULL DEFAULT 0,
    dirty             INTEGER NOT NULL DEFAULT 0
) STRICT;

CREATE INDEX list_status ON list_entry(status);
CREATE INDEX list_dirty  ON list_entry(dirty) WHERE dirty = 1;

CREATE TABLE playback (
    anime_id      INTEGER NOT NULL REFERENCES anime(id) ON DELETE CASCADE,
    ep_key        TEXT    NOT NULL,
    position_s    REAL    NOT NULL DEFAULT 0,
    duration_s    REAL,
    watched       INTEGER NOT NULL DEFAULT 0,
    synced        INTEGER NOT NULL DEFAULT 0,
    play_count    INTEGER NOT NULL DEFAULT 0,
    last_played_at INTEGER NOT NULL,
    PRIMARY KEY (anime_id, ep_key)
) STRICT;

CREATE INDEX playback_recent ON playback(last_played_at DESC);

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

CREATE TABLE torrent (
    info_hash   TEXT PRIMARY KEY,
    rqbit_id    INTEGER,
    name        TEXT    NOT NULL,
    total_bytes INTEGER NOT NULL DEFAULT 0,
    indexer     TEXT,
    magnet      TEXT,
    state       TEXT    NOT NULL DEFAULT 'idle',
    added_at    INTEGER NOT NULL
) STRICT;

-- Only files we actually mapped to an episode are downloaded; the rest of a
-- season batch is never fetched.
CREATE TABLE torrent_file (
    info_hash  TEXT    NOT NULL REFERENCES torrent(info_hash) ON DELETE CASCADE,
    file_index INTEGER NOT NULL,
    path       TEXT    NOT NULL,
    size_bytes INTEGER NOT NULL,
    anime_id   INTEGER REFERENCES anime(id) ON DELETE SET NULL,
    ep_key     TEXT,
    selected   INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (info_hash, file_index)
) STRICT;

CREATE INDEX tfile_episode ON torrent_file(anime_id, ep_key) WHERE anime_id IS NOT NULL;

-- Eviction is whole-file and oldest-first. bytes_on_disk comes from rqbit's
-- stats, never from the filesystem, because sparse files misreport their size.
CREATE TABLE cache_entry (
    info_hash      TEXT    NOT NULL,
    file_index     INTEGER NOT NULL,
    bytes_on_disk  INTEGER NOT NULL DEFAULT 0,
    complete       INTEGER NOT NULL DEFAULT 0,
    pinned         INTEGER NOT NULL DEFAULT 0,
    last_played_at INTEGER NOT NULL,
    PRIMARY KEY (info_hash, file_index),
    FOREIGN KEY (info_hash, file_index)
        REFERENCES torrent_file(info_hash, file_index) ON DELETE CASCADE
) STRICT;

CREATE INDEX cache_evict ON cache_entry(pinned, last_played_at);

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

-- Local mirror of SeaDex, refreshed weekly in a handful of requests.
CREATE TABLE seadex (
    anime_id      INTEGER NOT NULL,
    info_hash     TEXT    NOT NULL,
    release_group TEXT,
    tracker       TEXT,
    is_best       INTEGER NOT NULL DEFAULT 0,
    dual_audio    INTEGER NOT NULL DEFAULT 0,
    tags          TEXT    NOT NULL DEFAULT '[]',
    url           TEXT,
    files         TEXT    NOT NULL DEFAULT '[]',
    PRIMARY KEY (anime_id, info_hash)
) STRICT;

CREATE INDEX seadex_hash ON seadex(info_hash);

-- Auto-download is per show, not a global switch: you follow the ones you care about.
CREATE TABLE follow (
    anime_id     INTEGER PRIMARY KEY REFERENCES anime(id) ON DELETE CASCADE,
    quality      TEXT,
    group_filter TEXT,
    max_bytes    INTEGER,
    last_grabbed INTEGER,
    created_at   INTEGER NOT NULL
) STRICT;

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

CREATE TABLE setting (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
) STRICT;

CREATE TABLE home_widget (
    kind     TEXT PRIMARY KEY,
    position INTEGER NOT NULL,
    visible  INTEGER NOT NULL DEFAULT 1
) STRICT;

-- Conditional-request cache so metadata refreshes cost 304s instead of payloads.
CREATE TABLE http_cache (
    url        TEXT PRIMARY KEY,
    etag       TEXT,
    body       BLOB    NOT NULL,
    fetched_at INTEGER NOT NULL,
    expires_at INTEGER
) STRICT;

INSERT INTO setting (key, value) VALUES
    ('cache.budget_bytes',      '42949672960'),
    ('cache.prefetch_full',     'true'),
    ('quality.ladder',          '["1080p:BD:HEVC:10","1080p:BD","1080p:WEB","720p"]'),
    ('quality.max_auto_bytes',  '3221225472'),
    ('quality.allow_hi10p',     'false'),
    ('sync.progress_at',        '0.85'),
    ('sync.poll_seconds',       '900'),
    ('sync.add_missing',        'true'),
    ('player.local',            'mpv'),
    ('player.anime4k',          'true'),
    ('autodownload.enabled',    'false');

INSERT INTO home_widget (kind, position, visible) VALUES
    ('hero',             0, 1),
    ('continue',         1, 1),
    ('aired_unwatched',  2, 1),
    ('recently_cached',  3, 1),
    ('this_season',      4, 1),
    ('planning',         5, 1);

-- +goose Down
DROP TABLE IF EXISTS http_cache;
DROP TABLE IF EXISTS home_widget;
DROP TABLE IF EXISTS setting;
DROP TABLE IF EXISTS job;
DROP TABLE IF EXISTS follow;
DROP TABLE IF EXISTS seadex;
DROP TABLE IF EXISTS release;
DROP TABLE IF EXISTS cache_entry;
DROP TABLE IF EXISTS torrent_file;
DROP TABLE IF EXISTS torrent;
DROP TABLE IF EXISTS watch_session;
DROP TABLE IF EXISTS playback;
DROP TABLE IF EXISTS list_entry;
DROP TABLE IF EXISTS skip_time;
DROP TABLE IF EXISTS filler;
DROP TABLE IF EXISTS episode;
DROP TRIGGER IF EXISTS anime_fts_au;
DROP TRIGGER IF EXISTS anime_fts_ad;
DROP TRIGGER IF EXISTS anime_fts_ai;
DROP TABLE IF EXISTS anime_fts;
DROP TABLE IF EXISTS anime;
