-- +goose Up

-- Private trackers publish no usable infohash, so several curated releases for
-- one anime share an empty hash and collided under the old primary key. The
-- release group is what distinguishes them.
DROP TABLE IF EXISTS seadex;

CREATE TABLE seadex (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    anime_id      INTEGER NOT NULL,
    info_hash     TEXT    NOT NULL DEFAULT '',
    release_group TEXT,
    tracker       TEXT,
    is_best       INTEGER NOT NULL DEFAULT 0,
    dual_audio    INTEGER NOT NULL DEFAULT 0,
    tags          TEXT    NOT NULL DEFAULT '[]',
    url           TEXT,
    UNIQUE (anime_id, info_hash, release_group)
) STRICT;

CREATE INDEX seadex_anime ON seadex(anime_id);
CREATE INDEX seadex_hash  ON seadex(info_hash) WHERE info_hash <> '';

-- Forces a re-mirror so the corrected rows land.
DELETE FROM corpus_source WHERE name = 'seadex';

-- +goose Down
DROP TABLE IF EXISTS seadex;
