package store

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"
)

// Brings a download the engine holds under the cache budget.
func (s *Store) TrackTorrent(ctx context.Context, infoHash string, rqbitID int, name string) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := time.Now().Unix()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO torrent (info_hash, rqbit_id, name, total_bytes, state, added_at)
		VALUES (?,?,?,0,'live',?)
		ON CONFLICT(info_hash) DO UPDATE SET rqbit_id = excluded.rqbit_id`,
		infoHash, rqbitID, name, now); err != nil {
		return err
	}

	// cache_entry has a foreign key onto torrent_file.
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO torrent_file (info_hash, file_index, path, size_bytes, selected)
		VALUES (?,?,?,0,1)
		ON CONFLICT(info_hash, file_index) DO NOTHING`,
		infoHash, WholeTorrent, name); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO cache_entry (info_hash, file_index, bytes_on_disk, pinned, last_played_at)
		VALUES (?,?,0,0,?)
		ON CONFLICT(info_hash, file_index) DO NOTHING`,
		infoHash, WholeTorrent, now); err != nil {
		return err
	}
	return tx.Commit()
}

// File index for a download nobody has played, where no single file applies.
const WholeTorrent = -1

func (s *Store) DropTorrentCache(ctx context.Context, infoHash string) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM cache_entry WHERE info_hash = ?`, infoHash); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM torrent WHERE info_hash = ?`, infoHash); err != nil {
		return err
	}
	return tx.Commit()
}

type CacheEntry struct {
	InfoHash  string `json:"infoHash"`
	FileIndex int    `json:"fileIndex"`
	RqbitID   *int   `json:"rqbitId,omitempty"`
	Bytes     int64  `json:"bytes"`
	Complete  bool   `json:"complete"`
	Pinned    bool   `json:"pinned"`
	// Kept is a download someone asked for: outside the budget, never evicted.
	Kept       bool   `json:"kept"`
	Protected  bool   `json:"protected"`
	AnimeID    *int   `json:"animeId,omitempty"`
	EpKey      string `json:"epKey,omitempty"`
	Title      string `json:"title,omitempty"`
	LastPlayed int64  `json:"lastPlayed"`
}

// Bytes is what the budget sees; kept downloads are reported beside it.
type CacheUsage struct {
	Bytes     int64        `json:"bytes"`
	Budget    int64        `json:"budget"`
	Files     int          `json:"files"`
	Pinned    int          `json:"pinned"`
	Kept      int          `json:"kept"`
	KeptBytes int64        `json:"keptBytes"`
	Entries   []CacheEntry `json:"entries,omitempty"`
}

// torrent_file is joined loosely: only downloads that went through playback
// have one, and requiring it hid the rest from the budget.
const cacheListQuery = `
SELECT c.info_hash, c.file_index, t.rqbit_id, c.bytes_on_disk, c.complete, c.pinned, c.kept,
       CASE WHEN e.status IN ('CURRENT','REPEATING') THEN 1 ELSE 0 END,
       f.anime_id, coalesce(f.ep_key, ''), coalesce(a.title_romaji, t.name),
       c.last_played_at
FROM cache_entry c
JOIN torrent t           ON t.info_hash = c.info_hash
LEFT JOIN torrent_file f ON f.info_hash = c.info_hash AND f.file_index = c.file_index
LEFT JOIN anime a        ON a.id = f.anime_id
LEFT JOIN list_entry e   ON e.anime_id = f.anime_id
ORDER BY c.pinned DESC, c.last_played_at DESC`

func (s *Store) CacheEntries(ctx context.Context) ([]CacheEntry, error) {
	rows, err := s.r.QueryContext(ctx, cacheListQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []CacheEntry{}
	for rows.Next() {
		var (
			e                                 CacheEntry
			complete, pinned, kept, protected int
		)
		if err := rows.Scan(&e.InfoHash, &e.FileIndex, &e.RqbitID, &e.Bytes,
			&complete, &pinned, &kept, &protected, &e.AnimeID, &e.EpKey, &e.Title,
			&e.LastPlayed); err != nil {
			return nil, err
		}
		e.Complete, e.Pinned, e.Kept, e.Protected = complete == 1, pinned == 1, kept == 1, protected == 1
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) CacheUsage(ctx context.Context) (CacheUsage, error) {
	entries, err := s.CacheEntries(ctx)
	if err != nil {
		return CacheUsage{}, err
	}

	prefs, err := s.Prefs(ctx, 0)
	if err != nil {
		return CacheUsage{}, err
	}

	usage := CacheUsage{Budget: prefs.Int64("cache.budget_bytes"), Files: len(entries)}
	for _, e := range entries {
		if e.Pinned {
			usage.Pinned++
		}
		if e.Kept {
			usage.Kept++
			usage.KeptBytes += e.Bytes
			continue
		}
		usage.Bytes += e.Bytes
	}
	usage.Entries = entries
	return usage, nil
}

// KeepDownload moves a whole torrent between the cache tier and the kept tier.
// Whole because eviction deletes whole torrents.
// KeepFile moves one file of a pack between the tiers; the pack row keeps
// whatever any of its files is.
func (s *Store) KeepFile(ctx context.Context, infoHash string, fileIndex int, kept bool) (bool, error) {
	res, err := s.w.ExecContext(ctx,
		`UPDATE cache_entry SET kept = ? WHERE info_hash = ? AND file_index = ?`,
		boolInt(kept), infoHash, fileIndex)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

type DownloadFile struct {
	FileIndex int    `json:"fileIndex"`
	EpKey     string `json:"epKey"`
	Kept      bool   `json:"kept"`
	Complete  bool   `json:"complete"`
}

// DownloadFiles lists a torrent's episodes, for the pack row on Downloads.
func (s *Store) DownloadFiles(ctx context.Context, infoHash string) ([]DownloadFile, error) {
	rows, err := s.r.QueryContext(ctx, `
		SELECT f.file_index, coalesce(f.ep_key, ''), coalesce(c.kept, 0), coalesce(c.complete, 0)
		FROM torrent_file f
		LEFT JOIN cache_entry c ON c.info_hash = f.info_hash AND c.file_index = f.file_index
		WHERE f.info_hash = ? AND f.selected = 1
		ORDER BY CAST(f.ep_key AS INTEGER), f.file_index`, infoHash)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []DownloadFile{}
	for rows.Next() {
		var f DownloadFile
		var kept, complete int
		if err := rows.Scan(&f.FileIndex, &f.EpKey, &kept, &complete); err != nil {
			return nil, err
		}
		f.Kept, f.Complete = kept == 1, complete == 1
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Store) KeepDownload(ctx context.Context, infoHash string, kept bool) (bool, error) {
	res, err := s.w.ExecContext(ctx,
		`UPDATE cache_entry SET kept = ? WHERE info_hash = ?`, boolInt(kept), infoHash)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// Superseded lists releases another one replaced for the episode: every file of
// theirs is deselected, so nothing plays from them any more.
func (s *Store) Superseded(ctx context.Context, animeID int, epKey string) ([]string, error) {
	rows, err := s.r.QueryContext(ctx, `
		SELECT DISTINCT f.info_hash FROM torrent_file f
		WHERE f.anime_id = ? AND f.ep_key = ? AND f.selected = 0
		  AND NOT EXISTS (SELECT 1 FROM torrent_file s
		                  WHERE s.info_hash = f.info_hash AND s.selected = 1 AND s.file_index <> ?)`,
		animeID, epKey, WholeTorrent)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var hash string
		if err := rows.Scan(&hash); err != nil {
			return nil, err
		}
		out = append(out, hash)
	}
	return out, rows.Err()
}

// WatchedEpisodes: finished in the player, or below the list's progress and
// never played here. One gone back to and left unfinished is not seen.
func (s *Store) WatchedEpisodes(ctx context.Context, animeID int) (map[int]bool, error) {
	out := map[int]bool{}
	played := map[int]bool{}
	rows, err := s.r.QueryContext(ctx,
		`SELECT ep_key, watched FROM playback WHERE anime_id = ?`, animeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var watched bool
		if err := rows.Scan(&key, &watched); err != nil {
			return nil, err
		}
		if n, err := strconv.Atoi(key); err == nil {
			played[n] = true
			if watched {
				out[n] = true
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var progress int
	err = s.r.QueryRowContext(ctx,
		`SELECT coalesce(progress, 0) FROM list_entry WHERE anime_id = ?`, animeID).Scan(&progress)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	for n := 1; n <= progress; n++ {
		if !played[n] {
			out[n] = true
		}
	}
	return out, nil
}

func (s *Store) SetCacheBytes(ctx context.Context, infoHash string, fileIndex int, bytes int64, complete bool) error {
	_, err := s.w.ExecContext(ctx, `
		UPDATE cache_entry SET bytes_on_disk = ?, complete = ?
		WHERE info_hash = ? AND file_index = ?`,
		bytes, boolInt(complete), infoHash, fileIndex)
	return err
}

func (s *Store) PinCache(ctx context.Context, infoHash string, fileIndex int, pinned bool) error {
	_, err := s.w.ExecContext(ctx, `
		UPDATE cache_entry SET pinned = ?, last_played_at = ?
		WHERE info_hash = ? AND file_index = ?`,
		boolInt(pinned), time.Now().Unix(), infoHash, fileIndex)
	return err
}

// UnpinAll runs at startup: a pin left behind by a crash would otherwise be
// permanent and slowly fill the cache with unevictable files.
func (s *Store) UnpinAll(ctx context.Context) error {
	_, err := s.w.ExecContext(ctx, `UPDATE cache_entry SET pinned = 0`)
	return err
}

// PinOnly marks one file as the thing being watched and clears every other
// pin. Only one episode plays at a time, so the pin belongs to whatever last
// started streaming.
func (s *Store) PinOnly(ctx context.Context, infoHash string, fileIndex int) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`UPDATE cache_entry SET pinned = 0 WHERE pinned = 1 AND NOT (info_hash = ? AND file_index = ?)`,
		infoHash, fileIndex); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE cache_entry SET pinned = 1, last_played_at = ?
		WHERE info_hash = ? AND file_index = ?`,
		time.Now().Unix(), infoHash, fileIndex); err != nil {
		return err
	}
	return tx.Commit()
}

// CachedFile finds the file holding one episode, so playback can release its
// pin without tracking the hash itself.
func (s *Store) CachedFile(ctx context.Context, animeID int, epKey string) (string, int, error) {
	var hash string
	var index int
	err := s.r.QueryRowContext(ctx, `
		SELECT info_hash, file_index FROM torrent_file
		WHERE anime_id = ? AND ep_key = ? AND selected = 1
		LIMIT 1`, animeID, epKey).Scan(&hash, &index)
	if err != nil {
		return "", 0, nil
	}
	return hash, index, nil
}

// FileComplete answers from the sweeper's record, which outlives the engine
// session an episode was downloaded in.
func (s *Store) FileComplete(ctx context.Context, infoHash string, fileIndex int) bool {
	var complete int
	err := s.r.QueryRowContext(ctx,
		`SELECT complete FROM cache_entry WHERE info_hash = ? AND file_index = ?`,
		infoHash, fileIndex).Scan(&complete)
	return err == nil && complete == 1
}

func (s *Store) DropCacheEntry(ctx context.Context, infoHash string, fileIndex int) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM cache_entry WHERE info_hash = ? AND file_index = ?`,
		infoHash, fileIndex); err != nil {
		return err
	}
	// The torrent row is only useful while a file of it is cached.
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM torrent WHERE info_hash = ?
		AND NOT EXISTS (SELECT 1 FROM cache_entry WHERE info_hash = ?)`,
		infoHash, infoHash); err != nil {
		return err
	}
	return tx.Commit()
}
