-- +goose Up

-- list_entry.dirty is a single flag holding AniList's push state, which cannot
-- express "sent to AniList, not yet sent to MyAnimeList". This records what
-- each tracker was last told, so a pending push is a comparison rather than a
-- flag that two writers would fight over.
CREATE TABLE tracker_push (
    tracker   TEXT    NOT NULL,
    anime_id  INTEGER NOT NULL REFERENCES anime(id) ON DELETE CASCADE,
    progress  INTEGER NOT NULL DEFAULT 0,
    status    TEXT    NOT NULL DEFAULT '',
    pushed_at INTEGER NOT NULL,
    PRIMARY KEY (tracker, anime_id)
) STRICT;

CREATE INDEX tracker_push_anime ON tracker_push(anime_id);

-- +goose Down
DROP TABLE IF EXISTS tracker_push;
