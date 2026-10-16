-- +goose Up

-- Files already on disk. No foreign key: a scan matches corpus anime the anime table may not hold.
CREATE TABLE local_file (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    path          TEXT    NOT NULL UNIQUE,
    size          INTEGER NOT NULL,
    modified      INTEGER NOT NULL,
    anime_id      INTEGER,
    episode       INTEGER,
    season        INTEGER,
    parsed_title  TEXT,
    resolution    TEXT,
    release_group TEXT,
    confidence    REAL    NOT NULL DEFAULT 0,
    scanned_at    INTEGER NOT NULL,
    missing       INTEGER NOT NULL DEFAULT 0
) STRICT;

CREATE INDEX local_file_episode ON local_file(anime_id, episode) WHERE missing = 0;
CREATE INDEX local_file_missing ON local_file(missing);

-- +goose Down
DROP TABLE IF EXISTS local_file;
