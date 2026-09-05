-- +goose Up

-- AniList models each season as its own entry linked by PREQUEL/SEQUEL edges.
-- Walking those edges is what turns four rows into one franchise with a season
-- selector, and what lets an absolute episode number find its season.
CREATE TABLE relation (
    anime_id   INTEGER NOT NULL,
    related_id INTEGER NOT NULL,
    kind       TEXT    NOT NULL,
    PRIMARY KEY (anime_id, related_id, kind)
) STRICT;

CREATE INDEX relation_related ON relation(related_id);
CREATE INDEX relation_kind    ON relation(anime_id, kind);

-- Resolved franchise membership, recomputed whenever relations change. Storing
-- it avoids walking the graph on every page load.
CREATE TABLE franchise (
    anime_id INTEGER PRIMARY KEY,
    root_id  INTEGER NOT NULL,
    ordinal  INTEGER NOT NULL DEFAULT 0
) STRICT;

CREATE INDEX franchise_root ON franchise(root_id, ordinal);

-- +goose Down
DROP TABLE IF EXISTS franchise;
DROP TABLE IF EXISTS relation;
