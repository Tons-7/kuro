-- +goose Up

-- Its own column: an episode can be filler and a recap at once, and recaps come from another source.
ALTER TABLE filler ADD COLUMN recap INTEGER NOT NULL DEFAULT 0;
ALTER TABLE filler ADD COLUMN recap_source TEXT;

-- +goose Down
