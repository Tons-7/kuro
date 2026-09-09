-- +goose Up

-- When a franchise was last walked. Relations were fetched once and never
-- again, so a sequel or film announced later never appeared on the page.
CREATE TABLE relation_fetch (
    anime_id   INTEGER PRIMARY KEY,
    fetched_at INTEGER NOT NULL
) STRICT;

-- +goose Down
DROP TABLE IF EXISTS relation_fetch;
