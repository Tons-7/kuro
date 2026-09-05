package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type DirtyEntry struct {
	ID          int
	AnimeID     int
	Status      string
	Progress    int
	Episodes    int
	StartedAt   string
	CompletedAt string
	// Repeat is how many full rewatches are finished, what AniList calls
	// repeat and MAL num_times_rewatched.
	Repeat int
	// Score on kuro's 0-100 scale; 0 is unrated.
	Score int
}

// MarkWatched records local progress and flags the row for push. Progress only
// ever moves forward, so rewatching an episode can't reset the count.
func (s *Store) MarkWatched(ctx context.Context, animeID, episode int) (bool, error) {
	if animeID == 0 || episode <= 0 {
		return false, nil
	}
	if err := s.EnsureAnime(ctx, animeID); err != nil {
		return false, err
	}

	now := time.Now().Unix()
	today := time.Now().Format("2006-01-02")

	res, err := s.w.ExecContext(ctx, `
		INSERT INTO list_entry (id, anime_id, status, progress, started_at,
		                        remote_updated_at, local_updated_at, dirty)
		VALUES ((SELECT min(coalesce(min(id), 0), 0) - 1 FROM list_entry), ?, 'CURRENT', ?, ?, 0, ?, 1)
		ON CONFLICT(anime_id) DO UPDATE SET
		    progress = max(list_entry.progress, excluded.progress),
		    status = CASE
		        WHEN list_entry.status IN ('COMPLETED','REPEATING') THEN list_entry.status
		        ELSE 'CURRENT' END,
		    started_at = coalesce(list_entry.started_at, excluded.started_at),
		    local_updated_at = excluded.local_updated_at,
		    dirty = CASE WHEN excluded.progress > list_entry.progress THEN 1 ELSE list_entry.dirty END
		WHERE excluded.progress > list_entry.progress
		   OR list_entry.status IS NULL`,
		animeID, episode, today, now)
	if err != nil {
		return false, err
	}

	// Finishing the last episode completes the entry. AniList applies no such
	// rule server-side, so every client has to do it. Finishing a rewatch also
	// counts it, which is what the trackers' "rewatched N times" is.
	if _, err := s.w.ExecContext(ctx, `
		UPDATE list_entry SET
		    repeat_count = repeat_count + CASE WHEN status = 'REPEATING' THEN 1 ELSE 0 END,
		    status = 'COMPLETED', completed_at = ?, dirty = 1
		WHERE anime_id = ?
		  AND status NOT IN ('COMPLETED')
		  AND progress > 0
		  AND progress >= (SELECT episode_count FROM anime WHERE id = ? AND episode_count > 0)`,
		today, animeID, animeID); err != nil {
		return false, err
	}

	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ListStatuses is AniList's vocabulary, stored verbatim so a sync never has to
// translate and lose a distinction (MAL has no separate rewatching status).
var ListStatuses = []string{"CURRENT", "COMPLETED", "PAUSED", "DROPPED", "PLANNING", "REPEATING"}

const (
	StatusCurrent   = "CURRENT"
	StatusRepeating = "REPEATING"
)

func ValidStatus(status string) bool {
	for _, s := range ListStatuses {
		if s == status {
			return true
		}
	}
	return false
}

// Its own flag, not watched: dismissing is not a claim the episode was seen.
func (s *Store) DismissResume(ctx context.Context, animeID int, epKey string) error {
	_, err := s.w.ExecContext(ctx,
		`UPDATE playback SET dismissed = 1 WHERE anime_id = ? AND ep_key = ?`, animeID, epKey)
	return err
}

// Unwatch rewinds progress to the episode before: progress is a count on both
// trackers, so later episodes unmark too. A finished entry becomes current.
func (s *Store) Unwatch(ctx context.Context, animeID, episode int) error {
	if animeID == 0 || episode <= 0 {
		return nil
	}
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		UPDATE list_entry SET
		    progress = min(progress, ?2),
		    completed_at = CASE WHEN status = 'COMPLETED' THEN NULL ELSE completed_at END,
		    status = CASE WHEN status = 'COMPLETED' THEN 'CURRENT' ELSE status END,
		    local_updated_at = ?3, dirty = 1
		WHERE anime_id = ?1 AND (progress > ?2 OR status = 'COMPLETED')`,
		animeID, episode-1, time.Now().Unix()); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE playback SET watched = 0, position_s = 0, played_s = 0
		WHERE anime_id = ? AND ep_key GLOB '[0-9]*' AND CAST(ep_key AS INTEGER) >= ?`,
		animeID, episode); err != nil {
		return err
	}
	return tx.Commit()
}

// SetScore rates a show 0-100 (0 clears); an unlisted anime joins as PLANNING.
func (s *Store) SetScore(ctx context.Context, animeID, score int) error {
	if animeID == 0 || score < 0 || score > 100 {
		return nil
	}
	if err := s.EnsureAnime(ctx, animeID); err != nil {
		return err
	}
	_, err := s.w.ExecContext(ctx, `
		INSERT INTO list_entry (id, anime_id, status, progress, score, remote_updated_at, local_updated_at, dirty)
		VALUES ((SELECT min(coalesce(min(id), 0), 0) - 1 FROM list_entry), ?, 'PLANNING', 0, ?, 0, ?, 1)
		ON CONFLICT(anime_id) DO UPDATE SET
		    score = excluded.score,
		    local_updated_at = excluded.local_updated_at,
		    dirty = 1`,
		animeID, score, time.Now().Unix())
	return err
}

// RemoteEntry is what a tracker holds for one anime, in kuro's vocabulary.
type RemoteEntry struct {
	AnimeID  int
	Status   string
	Progress int
	Score    int
	Repeat   int
}

// ApplyRemote records a tracker-side change as a local edit (dirty), so the
// other tracker gets it. An unpushed local edit wins and is left alone.
func (s *Store) ApplyRemote(ctx context.Context, r RemoteEntry) (bool, error) {
	if r.AnimeID == 0 {
		return false, nil
	}
	if r.Status != "" && !ValidStatus(r.Status) {
		r.Status = ""
	}
	if err := s.EnsureAnime(ctx, r.AnimeID); err != nil {
		return false, err
	}

	var completed any
	if r.Status == "COMPLETED" {
		completed = time.Now().Format("2006-01-02")
	}
	res, err := s.w.ExecContext(ctx, `
		INSERT INTO list_entry (id, anime_id, status, progress, score, repeat_count, completed_at,
		                        remote_updated_at, local_updated_at, dirty)
		VALUES ((SELECT min(coalesce(min(id), 0), 0) - 1 FROM list_entry), ?1, nullif(?2, ''), ?3, ?4, ?5, ?6, 0, ?7, 1)
		ON CONFLICT(anime_id) DO UPDATE SET
		    status = coalesce(nullif(?2, ''), list_entry.status),
		    progress = ?3, score = ?4, repeat_count = max(list_entry.repeat_count, ?5),
		    completed_at = coalesce(?6, list_entry.completed_at),
		    local_updated_at = ?7, dirty = 1
		WHERE list_entry.dirty = 0`,
		r.AnimeID, r.Status, r.Progress, r.Score, r.Repeat, completed, time.Now().Unix())
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// SetListStatus records a status change and an optional score. A score of -1
// leaves whatever is there, since zero is a real score meaning "unrated".
func (s *Store) SetListStatus(ctx context.Context, animeID int, status string, score int) error {
	if animeID == 0 || status == "" {
		return nil
	}
	if err := s.EnsureAnime(ctx, animeID); err != nil {
		return err
	}

	now := time.Now().Unix()
	today := time.Now().Format("2006-01-02")

	// Marking something completed without a date leaves a gap the trackers
	// display as an empty finish date.
	var completed any
	if status == "COMPLETED" {
		completed = today
	}

	// A rewatch starts over: progress back to zero, so "continue" offers
	// episode 1 and the count climbs again; the finish date of the first watch
	// stays. Only on entering the status — re-choosing it mid-rewatch is a no-op.
	var previous sql.NullString
	if err := s.r.QueryRowContext(ctx,
		`SELECT status FROM list_entry WHERE anime_id = ?`, animeID).Scan(&previous); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	starting := status == StatusRepeating && previous.String != StatusRepeating

	_, err := s.w.ExecContext(ctx, `
		INSERT INTO list_entry (id, anime_id, status, progress, score, completed_at,
		                        remote_updated_at, local_updated_at, dirty)
		VALUES ((SELECT min(coalesce(min(id), 0), 0) - 1 FROM list_entry), ?, ?, 0,
		        CASE WHEN ?3 < 0 THEN 0 ELSE ?3 END, ?, 0, ?, 1)
		ON CONFLICT(anime_id) DO UPDATE SET
		    status = excluded.status,
		    progress = CASE WHEN ?6 THEN 0 ELSE list_entry.progress END,
		    score = CASE WHEN ?3 < 0 THEN list_entry.score ELSE ?3 END,
		    completed_at = coalesce(excluded.completed_at, list_entry.completed_at),
		    local_updated_at = excluded.local_updated_at,
		    dirty = 1`,
		animeID, status, score, completed, now, starting)
	if err != nil {
		return err
	}

	// Ticks and positions follow this pass: an episode left part way on the
	// first watch must not become the "continue" point of the rewatch.
	if starting {
		_, err = s.w.ExecContext(ctx,
			`UPDATE playback SET watched = 0, position_s = 0, dismissed = 0 WHERE anime_id = ?`, animeID)
	}
	return err
}

func (s *Store) DirtyEntries(ctx context.Context, limit int) ([]DirtyEntry, error) {
	rows, err := s.r.QueryContext(ctx, `
		SELECT e.id, e.anime_id, coalesce(e.status, ''), e.progress,
		       coalesce(a.episode_count, 0),
		       coalesce(e.started_at, ''), coalesce(e.completed_at, ''), e.repeat_count, e.score
		FROM list_entry e
		LEFT JOIN anime a ON a.id = e.anime_id
		WHERE e.dirty = 1 AND e.anime_id > 0
		ORDER BY e.local_updated_at
		LIMIT ?`, clampLimit(limit, 25, 200))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []DirtyEntry
	for rows.Next() {
		var d DirtyEntry
		if err := rows.Scan(&d.ID, &d.AnimeID, &d.Status, &d.Progress,
			&d.Episodes, &d.StartedAt, &d.CompletedAt, &d.Repeat, &d.Score); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ClearDirty stores the id and timestamp the server returned. Writing back the
// server's own updatedAt stops the next pull seeing our write as a remote change.
func (s *Store) ClearDirty(ctx context.Context, animeID, remoteID, remoteUpdatedAt int) error {
	_, err := s.w.ExecContext(ctx, `
		UPDATE list_entry
		SET dirty = 0, remote_updated_at = ?, id = coalesce(nullif(?, 0), id)
		WHERE anime_id = ?`, remoteUpdatedAt, remoteID, animeID)
	return err
}

// ListProgress maps anime to watched episode count for everything on the
// user's list, so a schedule can mark what is already seen.
func (s *Store) ListProgress(ctx context.Context) (map[int]int, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT anime_id, progress FROM list_entry`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[int]int, 512)
	for rows.Next() {
		var id, progress int
		if err := rows.Scan(&id, &progress); err != nil {
			return nil, err
		}
		out[id] = progress
	}
	return out, rows.Err()
}

func (s *Store) ListEntry(ctx context.Context, animeID int) (DirtyEntry, error) {
	var d DirtyEntry
	err := s.r.QueryRowContext(ctx, `
		SELECT e.id, e.anime_id, coalesce(e.status,''), e.progress,
		       coalesce(a.episode_count, 0), coalesce(e.started_at,''), coalesce(e.completed_at,''),
		       e.repeat_count, e.score
		FROM list_entry e
		LEFT JOIN anime a ON a.id = e.anime_id
		WHERE e.anime_id = ?`, animeID).Scan(
		&d.ID, &d.AnimeID, &d.Status, &d.Progress, &d.Episodes, &d.StartedAt, &d.CompletedAt, &d.Repeat, &d.Score)
	if errors.Is(err, sql.ErrNoRows) {
		return DirtyEntry{}, nil
	}
	return d, err
}
