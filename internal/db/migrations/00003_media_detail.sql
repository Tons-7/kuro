-- +goose Up

ALTER TABLE anime ADD COLUMN tags       TEXT NOT NULL DEFAULT '[]';
ALTER TABLE anime ADD COLUMN links      TEXT NOT NULL DEFAULT '[]';
ALTER TABLE anime ADD COLUMN cover_medium TEXT;
ALTER TABLE anime ADD COLUMN trailer_id   TEXT;
ALTER TABLE anime ADD COLUMN trailer_site TEXT;
ALTER TABLE anime ADD COLUMN source       TEXT;
ALTER TABLE anime ADD COLUMN mean_score   INTEGER;
ALTER TABLE anime ADD COLUMN favourites   INTEGER;
ALTER TABLE anime ADD COLUMN start_date   TEXT;
ALTER TABLE anime ADD COLUMN end_date     TEXT;

-- Artwork proxied locally, so scrolling is instant and the library renders offline.
CREATE TABLE image_cache (
    url        TEXT PRIMARY KEY,
    path       TEXT NOT NULL,
    bytes      INTEGER NOT NULL,
    mime       TEXT NOT NULL,
    fetched_at INTEGER NOT NULL
) STRICT;

-- +goose Down
DROP TABLE IF EXISTS image_cache;
