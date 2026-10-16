package store

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"slices"
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
	// Repeat is how many full rewatches are finished: AniList's repeat, MAL's num_times_rewatched.
	Repeat int
	// Score on kuro's 0-100 scale; 0 is unrated.
	Score int
	// ScoreCleared: set to unrated on purpose, so the push clears the site's.
	ScoreCleared bool
	Dirty        bool
	// Changed is local_updated_at when read; an edit since keeps it dirty.
	Changed int64
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
		    local_updated_at = max(excluded.local_updated_at, list_entry.local_updated_at + 1),
		    dirty = CASE WHEN excluded.progress > list_entry.progress THEN 1 ELSE list_entry.dirty END
		WHERE excluded.progress > list_entry.progress
		   OR list_entry.status IS NULL`,
		animeID, episode, today, now)
	if err != nil {
		return false, err
	}

	// Finishing the last episode completes the entry (AniList won't) and counts a rewatch.
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
	return slices.Contains(ListStatuses, status)
}

// DismissResume sets its own flag (dismissing is not watching); a new row's zero time keeps it out of history.
func (s *Store) DismissResume(ctx context.Context, animeID int, epKey string) error {
	_, err := s.w.ExecContext(ctx, `
		INSERT INTO playback (anime_id, ep_key, last_played_at, dismissed)
		VALUES (?, ?, 0, 1)
		ON CONFLICT(anime_id, ep_key) DO UPDATE SET dismissed = 1`, animeID, epKey)
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
		    local_updated_at = max(?3, local_updated_at + 1), dirty = 1
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
		    score_cleared = CASE WHEN excluded.score = 0 THEN 1 ELSE 0 END,
		    local_updated_at = max(excluded.local_updated_at, list_entry.local_updated_at + 1),
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
	// Since is the row's local_updated_at when the caller read it: an edit made after that wins.
	Since int64
}

// ApplyRemote records a tracker-side change as a local edit for the other tracker; a newer local edit wins.
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
		    local_updated_at = max(?7, list_entry.local_updated_at + 1), dirty = 1
		WHERE list_entry.local_updated_at <= ?8`,
		r.AnimeID, r.Status, r.Progress, r.Score, r.Repeat, completed, time.Now().Unix(), r.Since)
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

	// Marking something completed without a date leaves a gap the trackers display as an empty finish date.
	var completed any
	if status == "COMPLETED" {
		completed = today
	}

	// Entering a rewatch resets progress to zero but keeps the first finish date; re-choosing it is a no-op.
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
		    score_cleared = CASE WHEN ?3 = 0 THEN 1 WHEN ?3 > 0 THEN 0 ELSE list_entry.score_cleared END,
		    completed_at = coalesce(excluded.completed_at, list_entry.completed_at),
		    local_updated_at = max(excluded.local_updated_at, list_entry.local_updated_at + 1),
		    dirty = 1`,
		animeID, status, score, completed, now, starting)
	if err != nil {
		return err
	}

	// Ticks and positions follow this pass: an episode left part way on the
	// first watch must not become the "continue" point of the rewatch.
	if starting {
		_, err = s.w.ExecContext(ctx,
			`UPDATE playback SET watched = 0, position_s = 0, played_s = 0, dismissed = 0 WHERE anime_id = ?`, animeID)
	}
	return err
}

func (s *Store) DirtyEntries(ctx context.Context, limit int) ([]DirtyEntry, error) {
	rows, err := s.r.QueryContext(ctx, `
		SELECT e.id, e.anime_id, coalesce(e.status, ''), e.progress,
		       coalesce(a.episode_count, 0),
		       coalesce(e.started_at, ''), coalesce(e.completed_at, ''), e.repeat_count, e.score,
		       e.score_cleared, e.dirty, e.local_updated_at
		FROM list_entry e
		LEFT JOIN anime a ON a.id = e.anime_id
		WHERE e.dirty = 1 AND e.anime_id > 0
		ORDER BY e.anilist_failed_at, e.local_updated_at
		LIMIT ?`, clampLimit(limit, 25, 200))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []DirtyEntry
	for rows.Next() {
		var d DirtyEntry
		if err := rows.Scan(&d.ID, &d.AnimeID, &d.Status, &d.Progress,
			&d.Episodes, &d.StartedAt, &d.CompletedAt, &d.Repeat, &d.Score,
			&d.ScoreCleared, &d.Dirty, &d.Changed); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ClearDirty stores the id and timestamp the server returned. Writing back the
// server's own updatedAt stops the next pull seeing our write as a remote change.
func (s *Store) ClearDirty(ctx context.Context, animeID, remoteID, remoteUpdatedAt int) error {
	return s.ClearDirtyAt(ctx, animeID, remoteID, remoteUpdatedAt, math.MaxInt64)
}

// ClearDirtyAt clears dirty only if the row has not changed since changed.
func (s *Store) ClearDirtyAt(ctx context.Context, animeID, remoteID, remoteUpdatedAt int, changed int64) error {
	_, err := s.w.ExecContext(ctx, `
		UPDATE list_entry
		SET dirty = CASE WHEN local_updated_at <= ?1 THEN 0 ELSE dirty END,
		    score_cleared = CASE WHEN local_updated_at <= ?1 THEN 0 ELSE score_cleared END,
		    anilist_failed_at = 0,
		    remote_updated_at = ?2, id = coalesce(nullif(?3, 0), id)
		WHERE anime_id = ?4`, changed, remoteUpdatedAt, remoteID, animeID)
	return err
}

// PushFailed sends the entry to the back of tracker's push line ("anilist" or "mal") until a push succeeds.
func (s *Store) PushFailed(ctx context.Context, tracker string, animeID int) error {
	column := "anilist_failed_at"
	if tracker == "mal" {
		column = "mal_failed_at"
	}
	_, err := s.w.ExecContext(ctx, `UPDATE list_entry SET `+column+` = ? WHERE anime_id = ?`, time.Now().Unix(), animeID)
	return err
}

// ListProgress maps each listed anime to its watched count, so a schedule marks what's seen.
func (s *Store) ListProgress(ctx context.Context) (map[int]int, error) {
	entries, err := s.ListEntries(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[int]int, len(entries))
	for id, e := range entries {
		out[id] = e.Progress
	}
	return out, nil
}

// ListedEntry is what a card needs to show the list tag it already carries.
type ListedEntry struct {
	Status   string
	Progress int
}

func (s *Store) ListEntries(ctx context.Context) (map[int]ListedEntry, error) {
	rows, err := s.r.QueryContext(ctx,
		`SELECT anime_id, coalesce(status, ''), progress FROM list_entry`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[int]ListedEntry, 512)
	for rows.Next() {
		var id int
		var e ListedEntry
		if err := rows.Scan(&id, &e.Status, &e.Progress); err != nil {
			return nil, err
		}
		out[id] = e
	}
	return out, rows.Err()
}

func (s *Store) ListEntry(ctx context.Context, animeID int) (DirtyEntry, error) {
	var d DirtyEntry
	err := s.r.QueryRowContext(ctx, `
		SELECT e.id, e.anime_id, coalesce(e.status,''), e.progress,
		       coalesce(a.episode_count, 0), coalesce(e.started_at,''), coalesce(e.completed_at,''),
		       e.repeat_count, e.score, e.score_cleared, e.dirty, e.local_updated_at
		FROM list_entry e
		LEFT JOIN anime a ON a.id = e.anime_id
		WHERE e.anime_id = ?`, animeID).Scan(
		&d.ID, &d.AnimeID, &d.Status, &d.Progress, &d.Episodes, &d.StartedAt, &d.CompletedAt, &d.Repeat, &d.Score,
		&d.ScoreCleared, &d.Dirty, &d.Changed)
	if errors.Is(err, sql.ErrNoRows) {
		return DirtyEntry{}, nil
	}
	return d, err
}

// RemoveListEntry takes a show off the list: its entry, what each tracker was last told, and its follow.
func (s *Store) RemoveListEntry(ctx context.Context, animeID int) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM list_entry WHERE anime_id = ?`,
		`DELETE FROM tracker_push WHERE anime_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, animeID); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return s.SetFollow(ctx, animeID, false)
}
