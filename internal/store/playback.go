package store

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"

	"kuro/internal/player"
)

type TorrentRecord struct {
	InfoHash  string
	RqbitID   int
	Name      string
	TotalSize int64
	AnimeID   int
	EpKey     string
	FileIndex int
	FilePath  string
	// Chosen in the picker, so its name is not checked against the show.
	Manual bool
}

type PlaybackState struct {
	AnimeID  int
	EpKey    string
	Position float64
	Duration float64
	// Seconds of media played since the previous report. Seeks are not played.
	Played float64
}

// playedShare is how much of an episode must actually play before reaching the
// threshold counts, so tapping the end of the bar doesn't mark it watched.
const playedShare = 0.5

// A single report cannot honestly claim more than this; the players report
// every five to ten seconds.
const maxPlayedPerReport = 120.0

// finished is the one rule for "this episode is done": position reached the
// threshold, whatever the watched flag says. Shared by resume, the continue
// rail and the episode list so they agree.
func finished(position, duration float64, watched bool, threshold float64) bool {
	if duration > 0 {
		return position >= duration*threshold
	}
	return watched
}

// resumable is where a saved position is worth offering back.
func resumable(position, duration float64, watched bool, threshold float64) bool {
	// Resuming a few seconds in is worse than starting over.
	return position >= 15 && !finished(position, duration, watched, threshold)
}

// RecordTorrent links a torrent to the episode it holds, so a file we
// downloaded never has to be identified by its filename later.
func (s *Store) RecordTorrent(ctx context.Context, t TorrentRecord) error {
	// torrent_file.anime_id is a foreign key; a show never imported would fail it
	// and leave the download unlinked.
	if err := s.EnsureAnime(ctx, t.AnimeID); err != nil {
		return err
	}

	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := time.Now().Unix()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO torrent (info_hash, rqbit_id, name, total_bytes, state, added_at, manual)
		VALUES (?,?,?,?,'live',?,?)
		ON CONFLICT(info_hash) DO UPDATE SET
		    rqbit_id=excluded.rqbit_id, state='live',
		    manual=max(torrent.manual, excluded.manual)`,
		t.InfoHash, t.RqbitID, t.Name, t.TotalSize, now, boolInt(t.Manual)); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO torrent_file (info_hash, file_index, path, size_bytes, anime_id, ep_key, selected)
		VALUES (?,?,?,?,?,?,1)
		ON CONFLICT(info_hash, file_index) DO UPDATE SET
		    anime_id=excluded.anime_id, ep_key=excluded.ep_key, selected=1`,
		t.InfoHash, t.FileIndex, t.FilePath, t.TotalSize,
		nullableInt(t.AnimeID), nullable(t.EpKey)); err != nil {
		return err
	}

	// One selected release per episode: a prepare and a play can pick different
	// releases, and two selected rows leave lookups returning whichever the
	// ordering surfaces.
	if _, err := tx.ExecContext(ctx, `
		UPDATE torrent_file SET selected = 0
		WHERE anime_id = ? AND ep_key = ? AND NOT (info_hash = ? AND file_index = ?)`,
		nullableInt(t.AnimeID), nullable(t.EpKey), t.InfoHash, t.FileIndex); err != nil {
		return err
	}

	// Drop the whole-torrent placeholder or the bytes get counted twice.
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM cache_entry WHERE info_hash = ? AND file_index = ?`,
		t.InfoHash, WholeTorrent); err != nil {
		return err
	}

	// Recording a release is not watching it, so it is not pinned here: the pin
	// belongs to whatever actually starts streaming, which PinOnly sets.
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO cache_entry (info_hash, file_index, bytes_on_disk, pinned, last_played_at)
		VALUES (?,?,0,0,?)
		ON CONFLICT(info_hash, file_index) DO UPDATE SET
		    last_played_at=excluded.last_played_at`,
		t.InfoHash, t.FileIndex, now); err != nil {
		return err
	}

	// A kept episode fetched again under another release stays kept.
	if _, err := tx.ExecContext(ctx, `
		UPDATE cache_entry SET kept = 1
		WHERE info_hash = ? AND file_index = ? AND EXISTS (
		    SELECT 1 FROM cache_entry c
		    JOIN torrent_file f ON f.info_hash = c.info_hash AND f.file_index = c.file_index
		    WHERE f.anime_id = ? AND f.ep_key = ? AND c.kept = 1
		      AND NOT (c.info_hash = ? AND c.file_index = ?))`,
		t.InfoHash, t.FileIndex, nullableInt(t.AnimeID), nullable(t.EpKey),
		t.InfoHash, t.FileIndex); err != nil {
		return err
	}
	return tx.Commit()
}

// TorrentForEpisode returns the release already downloading or downloaded for an
// episode, avoiding a fresh indexer search — the slowest part of starting
// playback — when the answer is already on disk.
func (s *Store) TorrentForEpisode(ctx context.Context, animeID int, epKey string) (TorrentRecord, bool, error) {
	var t TorrentRecord
	var manual int
	err := s.r.QueryRowContext(ctx, `
		SELECT f.info_hash, coalesce(t.rqbit_id, 0), coalesce(t.name, ''),
		       coalesce(f.size_bytes, 0), f.file_index, coalesce(f.path, ''),
		       coalesce(t.manual, 0)
		FROM torrent_file f
		JOIN torrent t ON t.info_hash = f.info_hash
		WHERE f.anime_id = ? AND f.ep_key = ? AND f.selected = 1
		ORDER BY t.rqbit_id IS NULL, t.added_at DESC
		LIMIT 1`, animeID, epKey).
		Scan(&t.InfoHash, &t.RqbitID, &t.Name, &t.TotalSize, &t.FileIndex, &t.FilePath, &manual)
	if errors.Is(err, sql.ErrNoRows) {
		return TorrentRecord{}, false, nil
	}
	if err != nil {
		return TorrentRecord{}, false, err
	}
	t.AnimeID, t.EpKey, t.Manual = animeID, epKey, manual == 1
	return t, true, nil
}

// StartedTorrents is the set of info hashes, lower-cased, holding an episode
// somebody has begun watching.
func (s *Store) StartedTorrents(ctx context.Context) (map[string]bool, error) {
	rows, err := s.r.QueryContext(ctx, `
		SELECT DISTINCT lower(f.info_hash)
		FROM torrent_file f
		JOIN playback p ON p.anime_id = f.anime_id AND p.ep_key = f.ep_key
		WHERE f.selected = 1 AND p.position_s > 0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]bool{}
	for rows.Next() {
		var hash string
		if err := rows.Scan(&hash); err != nil {
			return nil, err
		}
		out[hash] = true
	}
	return out, rows.Err()
}

// ReconcileTorrents realigns stored rqbit ids with what the engine actually has.
// Ids are per-session, so a stale one would evict the wrong file; anything the
// engine no longer knows loses its id and is marked gone.
func (s *Store) ReconcileTorrents(ctx context.Context, live map[string]int) (matched, orphaned int, err error) {
	rows, err := s.r.QueryContext(ctx, `SELECT info_hash, rqbit_id FROM torrent`)
	if err != nil {
		return 0, 0, err
	}

	type row struct {
		hash string
		id   *int
	}
	var known []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.hash, &r.id); err != nil {
			rows.Close()
			return 0, 0, err
		}
		known = append(known, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}

	// Both sides normalised here: rqbit varies hash case, and a case difference
	// read as "gone" would orphan a live download.
	byHash := make(map[string]int, len(live))
	for hash, id := range live {
		byHash[strings.ToLower(hash)] = id
	}

	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()

	for _, r := range known {
		id, ok := byHash[strings.ToLower(r.hash)]
		if ok {
			if r.id == nil || *r.id != id {
				if _, err := tx.ExecContext(ctx,
					`UPDATE torrent SET rqbit_id = ?, state = 'live' WHERE info_hash = ?`,
					id, r.hash); err != nil {
					return matched, orphaned, err
				}
			}
			matched++
			continue
		}

		if _, err := tx.ExecContext(ctx,
			`UPDATE torrent SET rqbit_id = NULL, state = 'gone' WHERE info_hash = ?`,
			r.hash); err != nil {
			return matched, orphaned, err
		}
		orphaned++
	}
	return matched, orphaned, tx.Commit()
}

type Download struct {
	InfoHash string `json:"infoHash"`
	// Name is the release filename. Title is the show it belongs to, which is
	// what a row should lead with: a filename does not say what you downloaded.
	Name    string  `json:"name"`
	Title   string  `json:"title,omitempty"`
	Cover   *string `json:"cover,omitempty"`
	AnimeID int     `json:"animeId,omitempty"`
	// Episode is Episodes joined for display; a season pack carries several.
	Episode   string   `json:"episode,omitempty"`
	Episodes  []string `json:"episodes"`
	TotalSize int64    `json:"totalBytes"`
	OnDisk    int64    `json:"bytesOnDisk"`
	Percent   float64  `json:"percent"`
	Pinned    bool     `json:"pinned"`
	Kept      bool     `json:"kept"`
	State     string   `json:"state"`
	Paused    bool     `json:"paused"`
	// Checking: rqbit re-verifying the file after a launch.
	Checking bool    `json:"checking"`
	Mbps     float64 `json:"mbps,omitempty"`
	Peers    int     `json:"peers,omitempty"`
}

// DownloadStatus reports what the torrent engine is holding. Percentages come
// from the cache table, which the sweeper keeps current.
func (s *Store) DownloadStatus(ctx context.Context) ([]Download, error) {
	// Before the cursor opens: a query while rows are held drains the read pool.
	mode := s.TitleMode(ctx, 0)

	// One row per torrent: a season pack serves several episodes, folded in
	// episode order, and a pin on any of them means playing.
	rows, err := s.r.QueryContext(ctx, `
		SELECT t.info_hash, t.name, coalesce(t.state, ''),
		       max(coalesce(t.total_bytes, 0), coalesce(sum(f.size_bytes), 0)),
		       coalesce(max(f.anime_id), 0),
		       coalesce(group_concat(f.ep_key, ','), ''),
		       coalesce(sum(c.bytes_on_disk), 0), max(coalesce(c.pinned, 0)), max(coalesce(c.kept, 0)),
		       max(a.title_romaji), max(a.title_english), max(a.cover_url)
		FROM torrent t
		JOIN (
		    SELECT info_hash, file_index, anime_id, ep_key, size_bytes FROM torrent_file
		    WHERE selected = 1
		    ORDER BY info_hash, ep_key IS NULL, CAST(ep_key AS INTEGER), file_index
		) f ON f.info_hash = t.info_hash
		LEFT JOIN cache_entry c ON c.info_hash = f.info_hash AND c.file_index = f.file_index
		LEFT JOIN anime a ON a.id = f.anime_id
		GROUP BY t.info_hash
		ORDER BY t.added_at DESC
		LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Download{}
	for rows.Next() {
		var d Download
		var pinned, kept int
		var episodes string
		var romaji, english *string
		if err := rows.Scan(&d.InfoHash, &d.Name, &d.State, &d.TotalSize,
			&d.AnimeID, &episodes, &d.OnDisk, &pinned, &kept,
			&romaji, &english, &d.Cover); err != nil {
			return nil, err
		}
		d.Pinned, d.Kept = pinned == 1, kept == 1
		d.Episodes = []string{}
		if episodes != "" {
			d.Episodes = strings.Split(episodes, ",")
		}
		d.Episode = strings.Join(d.Episodes, ", ")
		if romaji != nil {
			d.Title = PickTitle(mode, *romaji, english)
		}
		if d.TotalSize > 0 {
			d.Percent = min(100, float64(d.OnDisk)/float64(d.TotalSize)*100)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ResumeAt returns where playback stopped, or zero once the episode was finished
// so a rewatch starts over. Finished is the position, not the watched flag.
func (s *Store) ResumeAt(ctx context.Context, animeID int, epKey string) (float64, error) {
	var position, duration float64
	var watched int
	err := s.r.QueryRowContext(ctx,
		`SELECT position_s, coalesce(duration_s, 0), watched FROM playback WHERE anime_id = ? AND ep_key = ?`,
		animeID, epKey).Scan(&position, &duration, &watched)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if !resumable(position, duration, watched == 1, s.thresholdFor(ctx, animeID)) {
		return 0, nil
	}
	return position, nil
}

// InProgress is the most recently played episode of a show worth going back to.
// The series page leads with it rather than the tracker's next episode, which a
// watched flag can have moved on already.
type InProgress struct {
	EpKey    string
	Position float64
	Duration float64
}

func (s *Store) LastInProgress(ctx context.Context, animeID int) (InProgress, bool, error) {
	var p InProgress
	var watched int
	err := s.r.QueryRowContext(ctx, `
		SELECT ep_key, position_s, coalesce(duration_s, 0), watched
		FROM playback
		WHERE anime_id = ? AND dismissed = 0
		ORDER BY last_played_at DESC
		LIMIT 1`, animeID).Scan(&p.EpKey, &p.Position, &p.Duration, &watched)
	if errors.Is(err, sql.ErrNoRows) {
		return InProgress{}, false, nil
	}
	if err != nil {
		return InProgress{}, false, err
	}
	if !resumable(p.Position, p.Duration, watched == 1, s.thresholdFor(ctx, animeID)) {
		return InProgress{}, false, nil
	}
	return p, true, nil
}

// SavePlayback records a report and says whether the episode now counts as
// watched. Decided here, once, for both players: the position has to reach
// the threshold and enough of the episode has to have actually played.
func (s *Store) SavePlayback(ctx context.Context, p PlaybackState) (bool, error) {
	threshold := s.thresholdFor(ctx, p.AnimeID)

	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	var (
		position, duration, played float64
		watched                    bool
		existed                    bool
	)
	var flag int
	err = tx.QueryRowContext(ctx,
		`SELECT position_s, coalesce(duration_s, 0), played_s, watched FROM playback WHERE anime_id = ? AND ep_key = ?`,
		p.AnimeID, p.EpKey).Scan(&position, &duration, &played, &flag)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return false, err
	default:
		existed, watched = true, flag == 1
	}

	if p.Duration > 0 {
		duration = p.Duration
	}
	step := max(0, min(p.Played, maxPlayedPerReport))
	played += step
	if step > 0 {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO watch_time (day, seconds) VALUES (?, ?)
			ON CONFLICT(day) DO UPDATE SET seconds = seconds + excluded.seconds`,
			time.Now().Format("2006-01-02"), step); err != nil {
			return false, err
		}
	}

	reached := duration > 0 && p.Position/duration >= threshold
	if reached && played >= duration*playedShare {
		watched = true
	}

	// A jump to the ending that did not earn the watched flag keeps its old resume
	// point, so a peek doesn't overwrite it with the credits.
	if !reached || watched || !existed {
		position = p.Position
	}

	// Counted per finish, not per report, and only real play time undoes a
	// dismissal — a flush on pause should not drag the episode back onto the rail.
	finished := boolInt(watched && !(existed && flag == 1))
	resumed := boolInt(step > 0)

	_, err = tx.ExecContext(ctx, `
		INSERT INTO playback (anime_id, ep_key, position_s, duration_s, played_s, watched, play_count, last_played_at)
		VALUES (?,?,?,?,?,?,?,?)
		ON CONFLICT(anime_id, ep_key) DO UPDATE SET
		    position_s=excluded.position_s,
		    duration_s=coalesce(nullif(excluded.duration_s, 0), playback.duration_s),
		    played_s=excluded.played_s,
		    watched=max(playback.watched, excluded.watched),
		    dismissed=CASE WHEN ? = 1 THEN 0 ELSE playback.dismissed END,
		    play_count=playback.play_count + ?,
		    last_played_at=excluded.last_played_at`,
		p.AnimeID, p.EpKey, position, p.Duration, played, boolInt(watched), finished,
		time.Now().Unix(), resumed, finished)
	if err != nil {
		return false, err
	}
	return watched, tx.Commit()
}

// thresholdFor is the fraction of an episode that counts as watched, honouring
// a per-show override.
func (s *Store) thresholdFor(ctx context.Context, animeID int) float64 {
	prefs, err := s.Prefs(ctx, animeID)
	if err != nil {
		return defaultThreshold()
	}
	if v := prefs.Float("sync.progress_at"); v > 0 && v <= 1 {
		return v
	}
	return defaultThreshold()
}

func (s *Store) watchedThreshold(ctx context.Context) float64 {
	return s.thresholdFor(ctx, 0)
}

func defaultThreshold() float64 {
	v, err := strconv.ParseFloat(Defaults["sync.progress_at"], 64)
	if err != nil || v <= 0 || v > 1 {
		return 0.9
	}
	return v
}

// SkipRanges returns opening and ending markers for an episode. They are keyed
// by MAL id because that is what AniSkip indexes on.
func (s *Store) SkipRanges(ctx context.Context, animeID, episode int) ([]player.SkipRange, error) {
	rows, err := s.r.QueryContext(ctx, `
		SELECT k.kind, k.start_s, k.end_s
		FROM skip_time k
		JOIN anime a ON a.mal_id = k.mal_id
		WHERE a.id = ? AND k.number = ?
		ORDER BY k.start_s`, animeID, episode)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []player.SkipRange
	for rows.Next() {
		var r player.SkipRange
		if err := rows.Scan(&r.Kind, &r.Start, &r.End); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func nullableInt(v int) any {
	if v == 0 {
		return nil
	}
	return v
}
