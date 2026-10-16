-- +goose Up

-- PREQUEL/SEQUEL edges between seasons: walked, they make one franchise and place absolute episode numbers.
CREATE TABLE relation (
    anime_id   INTEGER NOT NULL,
    related_id INTEGER NOT NULL,
    kind       TEXT    NOT NULL,
    PRIMARY KEY (anime_id, related_id, kind)
) STRICT;

CREATE INDEX relation_related ON relation(related_id);
CREATE INDEX relation_kind    ON relation(anime_id, kind);

-- Franchise membership, recomputed when relations change instead of walked per page load.
CREATE TABLE franchise (
    anime_id INTEGER PRIMARY KEY,
    root_id  INTEGER NOT NULL,
    ordinal  INTEGER NOT NULL DEFAULT 0
) STRICT;

CREATE INDEX franchise_root ON franchise(root_id, ordinal);

-- +goose Down
DROP TABLE IF EXISTS franchise;
DROP TABLE IF EXISTS relation;
