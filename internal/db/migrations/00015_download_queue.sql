-- +goose Up
-- A table, so the download queue survives a restart, can be shown, and runs one at a time across shows.
CREATE TABLE download_queue (
    anime_id   INTEGER NOT NULL REFERENCES anime(id) ON DELETE CASCADE,
    ep_key     TEXT    NOT NULL,
    episode    INTEGER NOT NULL,
    season     INTEGER NOT NULL DEFAULT 1,
    -- pending | active | done | failed
    state      TEXT    NOT NULL DEFAULT 'pending',
    error      TEXT,
    queued_at  INTEGER NOT NULL,
    started_at INTEGER,
    PRIMARY KEY (anime_id, ep_key)
) STRICT;

-- The worker asks for the oldest pending row on every pass.
CREATE INDEX download_queue_pending ON download_queue (state, queued_at);

-- +goose Down
DROP TABLE download_queue;
