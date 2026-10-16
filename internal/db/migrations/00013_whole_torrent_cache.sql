-- +goose Up
-- Index -1 now means the whole torrent; adopted downloads at index 0 collided with a real first file.
DELETE FROM cache_entry
WHERE file_index = 0
  AND info_hash IN (SELECT info_hash FROM torrent_file WHERE file_index = 0 AND size_bytes = 0);

DELETE FROM torrent_file WHERE file_index = 0 AND size_bytes = 0;

-- +goose Down
SELECT 1;
