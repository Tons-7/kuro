-- +goose Up

-- rqbit was replaced by the built-in engine; the id is the engine's.
ALTER TABLE torrent RENAME COLUMN rqbit_id TO engine_id;

-- +goose Down
ALTER TABLE torrent RENAME COLUMN engine_id TO rqbit_id;
