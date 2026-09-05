-- +goose Up

-- Local-only state that has no AniList equivalent, kept out of list_entry so
-- a sync can never overwrite it.
CREATE TABLE user_anime (
    anime_id   INTEGER PRIMARY KEY REFERENCES anime(id) ON DELETE CASCADE,
    favourite  INTEGER NOT NULL DEFAULT 0,
    pinned     INTEGER NOT NULL DEFAULT 0,
    hidden     INTEGER NOT NULL DEFAULT 0,
    note       TEXT,
    updated_at INTEGER NOT NULL
) STRICT;

CREATE INDEX user_anime_fav ON user_anime(favourite) WHERE favourite = 1;

-- Per-show overrides of the global defaults, same keys as the setting table.
-- Sparse by design: a row exists only for what was actually changed.
CREATE TABLE anime_pref (
    anime_id INTEGER NOT NULL REFERENCES anime(id) ON DELETE CASCADE,
    key      TEXT    NOT NULL,
    value    TEXT    NOT NULL,
    PRIMARY KEY (anime_id, key)
) STRICT;

CREATE TABLE notification (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    kind       TEXT    NOT NULL,
    anime_id   INTEGER,
    episode    INTEGER,
    title      TEXT    NOT NULL,
    body       TEXT,
    payload    TEXT    NOT NULL DEFAULT '{}',
    created_at INTEGER NOT NULL,
    read_at    INTEGER
) STRICT;

CREATE INDEX notification_unread ON notification(read_at, created_at DESC);
CREATE INDEX notification_anime  ON notification(anime_id, created_at DESC);

-- Prevents re-announcing the same release on every poll.
CREATE TABLE notified_release (
    info_hash  TEXT PRIMARY KEY,
    anime_id   INTEGER,
    episode    INTEGER,
    created_at INTEGER NOT NULL
) STRICT;

INSERT INTO setting (key, value) VALUES
    ('playback.player',          'mpv'),
    ('playback.autoskip_op',     'true'),
    ('playback.autoskip_ed',     'false'),
    ('playback.autonext',        'true'),
    ('playback.anime4k',         'false'),
    ('playback.anime4k_mode',    'A'),
    ('playback.anime4k_size',    'M'),
    ('audio.prefer',             'sub'),
    ('notify.enabled',           'true'),
    ('notify.releases',          'sub'),
    ('notify.poll_seconds',      '1800');

-- +goose Down
DROP TABLE IF EXISTS notified_release;
DROP TABLE IF EXISTS notification;
DROP TABLE IF EXISTS anime_pref;
DROP TABLE IF EXISTS user_anime;
