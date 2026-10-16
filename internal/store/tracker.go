package store

import (
	"context"
	"database/sql"
	"errors"
	"slices"
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
	// LastScore is what the tracker was last told, so a 0 now is a clear.
	LastScore int
}

// sharesMALID: another entry on this MAL id speaks for it, or the two overwrite each other every cycle.
const sharesMALID = `
	SELECT 1 FROM list_entry o JOIN anime oa ON oa.id = o.anime_id
	WHERE oa.mal_id = a.mal_id AND o.anime_id <> e.anime_id
	  AND (o.local_updated_at > e.local_updated_at OR (o.local_updated_at = e.local_updated_at AND o.anime_id < e.anime_id))`

// pendingMAL selects entries whose local progress or status differs from what
// MyAnimeList was last told. Anime with no MAL id are skipped; animeID 0 means all.
const pendingMAL = `
	SELECT e.anime_id, a.mal_id, coalesce(e.status, ''), e.progress,
	       coalesce(a.episode_count, 0), e.repeat_count, e.score, coalesce(p.score, 0)
	FROM list_entry e
	JOIN anime a ON a.id = e.anime_id
	LEFT JOIN tracker_push p ON p.tracker = ? AND p.anime_id = e.anime_id
	WHERE a.mal_id IS NOT NULL AND a.mal_id > 0
	  AND (?2 = 0 OR e.anime_id = ?2)
	  AND NOT EXISTS (` + sharesMALID + `)
	  AND (p.anime_id IS NULL
	       OR p.progress <> e.progress
	       OR p.status <> coalesce(e.status, '')
	       OR p.score <> e.score)
	  -- Nothing MAL can hold: no status it knows and no progress.
	  AND NOT (coalesce(e.status, '') = '' AND e.progress = 0)
	ORDER BY e.mal_failed_at, e.local_updated_at
	LIMIT ?3`

// PendingMALPush returns the one entry for animeID, or a zero value when MAL is already up to date on it.
func (s *Store) PendingMALPush(ctx context.Context, tracker string, animeID int) (TrackerEntry, error) {
	var e TrackerEntry
	err := s.r.QueryRowContext(ctx, pendingMAL, tracker, animeID, 1).Scan(
		&e.AnimeID, &e.RemoteID, &e.Status, &e.Progress, &e.Episodes, &e.Repeat, &e.Score, &e.LastScore)
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
		if err := rows.Scan(&e.AnimeID, &e.RemoteID, &e.Status, &e.Progress, &e.Episodes, &e.Repeat, &e.Score, &e.LastScore); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ForgetPushed drops what a tracker was told, so the next account reads as new.
func (s *Store) ForgetPushed(ctx context.Context, tracker string) error {
	_, err := s.w.ExecContext(ctx, `DELETE FROM tracker_push WHERE tracker = ?`, tracker)
	return err
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
	if err == nil && tracker == "mal" {
		_, err = s.w.ExecContext(ctx, `UPDATE list_entry SET mal_failed_at = 0 WHERE anime_id = ? AND mal_failed_at <> 0`, animeID)
	}
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

// AniListIDsForMAL maps MAL ids to AniList ids in one scan, a shared id going where pendingMAL pushes it.
func (s *Store) AniListIDsForMAL(ctx context.Context, malIDs []int) (map[int]int, error) {
	out := make(map[int]int, len(malIDs))
	if len(malIDs) == 0 {
		return out, nil
	}

	wanted := make(map[int]bool, len(malIDs))
	for _, id := range malIDs {
		wanted[id] = true
	}

	candidates := map[int][]int{}
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
			if wanted[malID] && !slices.Contains(candidates[malID], animeID) {
				candidates[malID] = append(candidates[malID], animeID)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}

	for malID, ids := range candidates {
		if len(ids) == 1 {
			out[malID] = ids[0]
			continue
		}
		pick, err := s.speaksForMAL(ctx, ids)
		if err != nil {
			return nil, err
		}
		out[malID] = pick
	}
	return out, nil
}

// speaksForMAL picks among anime sharing a MAL id: the listed one edited last, else the lowest id.
func (s *Store) speaksForMAL(ctx context.Context, ids []int) (int, error) {
	slices.Sort(ids)
	pick, best := ids[0], int64(-1)
	for _, id := range ids {
		var changed int64
		err := s.r.QueryRowContext(ctx, `SELECT local_updated_at FROM list_entry WHERE anime_id = ?`, id).Scan(&changed)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return 0, err
		}
		if changed > best {
			pick, best = id, changed
		}
	}
	return pick, nil
}
