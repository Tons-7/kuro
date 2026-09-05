-- +goose Up

-- An episode can be filler and a recap at the same time, so recap cannot be
-- another value in the kind column. The filler dataset has no recap concept
-- at all; it comes from a separate source.
ALTER TABLE filler ADD COLUMN recap INTEGER NOT NULL DEFAULT 0;
ALTER TABLE filler ADD COLUMN recap_source TEXT;

-- +goose Down
