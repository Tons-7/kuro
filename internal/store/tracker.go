package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// TrackerEntry is one anime that a tracker has not been told about yet.
type TrackerEntry struct {
	AnimeID  int // AniList id, kuro's canonical key
	RemoteID int // the id this tracker uses
	Status   string
	Progress int
	Episodes int
	// Repeat is the finished-rewatch count, MAL's num_times_rewatched.
	Repeat int
	// Score on kuro's 0-100 scale; each tracker converts.
	Score int
}

// pendingMAL selects entries whose local progress or status differs from what
// MyAnimeList was last told. Anime with no MAL id are skipped; animeID 0 means all.
const pendingMAL = `
	SELECT e.anime_id, a.mal_id, coalesce(e.status, ''), e.progress,
	       coalesce(a.episode_count, 0), e.repeat_count, e.score
	FROM list_entry e
	JOIN anime a ON a.id = e.anime_id
	LEFT JOIN tracker_push p ON p.tracker = ? AND p.anime_id = e.anime_id
	WHERE a.mal_id IS NOT NULL AND a.mal_id > 0
	  AND (?2 = 0 OR e.anime_id = ?2)
	  AND (p.anime_id IS NULL
	       OR p.progress <> e.progress
	       OR p.status <> coalesce(e.status, '')
	       OR p.score <> e.score)
	ORDER BY e.local_updated_at
	LIMIT ?3`

// PendingMALPush returns the one entry for animeID, or a zero value when MAL
// is already up to date on it.
func (s *Store) PendingMALPush(ctx context.Context, tracker string, animeID int) (TrackerEntry, error) {
	var e TrackerEntry
	err := s.r.QueryRowContext(ctx, pendingMAL, tracker, animeID, 1).Scan(
		&e.AnimeID, &e.RemoteID, &e.Status, &e.Progress, &e.Episodes, &e.Repeat, &e.Score)
	if errors.Is(err, sql.ErrNoRows) {
		return TrackerEntry{}, nil
	}
	return e, err
}

func (s *Store) PendingMALPushes(ctx context.Context, tracker string, limit int) ([]TrackerEntry, error) {
	rows, err := s.r.QueryContext(ctx, pendingMAL, tracker, 0, clampLimit(limit, 25, 200))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []TrackerEntry
	for rows.Next() {
		var e TrackerEntry
		if err := rows.Scan(&e.AnimeID, &e.RemoteID, &e.Status, &e.Progress, &e.Episodes, &e.Repeat, &e.Score); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// MarkPushed records what the tracker now holds. Storing the value rather than
// clearing a flag means a later local change is detected by comparison.
func (s *Store) MarkPushed(ctx context.Context, tracker string, animeID, progress int, status string, score int) error {
	_, err := s.w.ExecContext(ctx, `
		INSERT INTO tracker_push (tracker, anime_id, progress, status, score, pushed_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(tracker, anime_id) DO UPDATE SET
		    progress = excluded.progress,
		    status = excluded.status,
		    score = excluded.score,
		    pushed_at = excluded.pushed_at`,
		tracker, animeID, progress, status, score, time.Now().Unix())
	return err
}

// Pushed is what the tracker was last told, or false when it never was.
func (s *Store) Pushed(ctx context.Context, tracker string, animeID int) (TrackerEntry, bool, error) {
	var e TrackerEntry
	err := s.r.QueryRowContext(ctx, `
		SELECT anime_id, progress, status, score FROM tracker_push
		WHERE tracker = ? AND anime_id = ?`, tracker, animeID).
		Scan(&e.AnimeID, &e.Progress, &e.Status, &e.Score)
	if errors.Is(err, sql.ErrNoRows) {
		return TrackerEntry{}, false, nil
	}
	return e, err == nil, err
}

// SeedPushed records a tracker's existing state without pushing anything, so
// connecting an account does not immediately re-send the whole list back to it.
func (s *Store) SeedPushed(ctx context.Context, tracker string, entries []TrackerEntry) (int, error) {
	if len(entries) == 0 {
		return 0, nil
	}

	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO tracker_push (tracker, anime_id, progress, status, score, pushed_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(tracker, anime_id) DO UPDATE SET
		    progress = excluded.progress,
		    status = excluded.status,
		    score = excluded.score,
		    pushed_at = excluded.pushed_at`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	now := time.Now().Unix()
	var n int
	for _, e := range entries {
		if e.AnimeID == 0 {
			continue
		}
		if _, err := stmt.ExecContext(ctx, tracker, e.AnimeID, e.Progress, e.Status, e.Score, now); err != nil {
			return n, err
		}
		n++
	}
	return n, tx.Commit()
}

// AniListIDsForMAL resolves MAL ids to kuro's canonical AniList ids, scanning
// the whole mapping once rather than one query per id.
func (s *Store) AniListIDsForMAL(ctx context.Context, malIDs []int) (map[int]int, error) {
	out := make(map[int]int, len(malIDs))
	if len(malIDs) == 0 {
		return out, nil
	}

	wanted := make(map[int]bool, len(malIDs))
	for _, id := range malIDs {
		wanted[id] = true
	}

	for _, query := range []string{
		`SELECT mal_id, anime_id FROM corpus_anime WHERE mal_id IS NOT NULL`,
		`SELECT mal_id, id FROM anime WHERE mal_id IS NOT NULL`,
	} {
		rows, err := s.r.QueryContext(ctx, query)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var malID, animeID int
			if err := rows.Scan(&malID, &animeID); err != nil {
				rows.Close()
				return nil, err
			}
			if wanted[malID] {
				out[malID] = animeID
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
