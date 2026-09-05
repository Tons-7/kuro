-- +goose Up
-- Queuing a season used to mean holding the whole list in a goroutine: nothing
-- was visible until each episode resolved, the order was invisible, and a
-- restart lost it. The queue is a table so it survives, can be shown, and can
-- be worked through one at a time across every show at once.
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
