-- +goose Up
-- Downloads adopted from the engine were recorded at file index 0, which
-- collides with a real first file. Index -1 now means the whole torrent, so a
-- placeholder and a played file can no longer be mistaken for each other.
DELETE FROM cache_entry
WHERE file_index = 0
  AND info_hash IN (SELECT info_hash FROM torrent_file WHERE file_index = 0 AND size_bytes = 0);

DELETE FROM torrent_file WHERE file_index = 0 AND size_bytes = 0;

-- +goose Down
SELECT 1;
