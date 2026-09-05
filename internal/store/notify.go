package store

import (
	"context"
	"encoding/json"
	"time"
)

type Notification struct {
	ID        int64          `json:"id"`
	Kind      string         `json:"kind"`
	AnimeID   *int           `json:"animeId,omitempty"`
	Episode   *int           `json:"episode,omitempty"`
	Title     string         `json:"title"`
	Body      string         `json:"body,omitempty"`
	Payload   map[string]any `json:"payload,omitempty"`
	CreatedAt int64          `json:"createdAt"`
	Read      bool           `json:"read"`
}

const (
	NotifyRelease = "release"
	NotifyAiring  = "airing"
	NotifyError   = "error"
	NotifyUpdate  = "update"
)

func (s *Store) AddNotification(ctx context.Context, n Notification) (int64, error) {
	payload := "{}"
	if len(n.Payload) > 0 {
		if b, err := json.Marshal(n.Payload); err == nil {
			payload = string(b)
		}
	}

	res, err := s.w.ExecContext(ctx, `
		INSERT INTO notification (kind, anime_id, episode, title, body, payload, created_at)
		VALUES (?,?,?,?,?,?,?)`,
		n.Kind, nullableInt(deref(n.AnimeID)), nullableInt(deref(n.Episode)),
		n.Title, nullable(n.Body), payload, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) Notifications(ctx context.Context, unreadOnly bool, limit int) ([]Notification, error) {
	rows, err := s.r.QueryContext(ctx, `
		SELECT id, kind, anime_id, episode, title, body, payload, created_at, read_at
		FROM notification
		WHERE (? = 0 OR read_at IS NULL)
		ORDER BY created_at DESC
		LIMIT ?`, boolInt(unreadOnly), clampLimit(limit, 50, 500))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Notification{}
	for rows.Next() {
		var (
			n       Notification
			body    *string
			payload string
			readAt  *int64
		)
		if err := rows.Scan(&n.ID, &n.Kind, &n.AnimeID, &n.Episode, &n.Title,
			&body, &payload, &n.CreatedAt, &readAt); err != nil {
			return nil, err
		}
		if body != nil {
			n.Body = *body
		}
		n.Read = readAt != nil
		json.Unmarshal([]byte(payload), &n.Payload)
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *Store) UnreadCount(ctx context.Context) (int, error) {
	var n int
	err := s.r.QueryRowContext(ctx,
		`SELECT count(*) FROM notification WHERE read_at IS NULL`).Scan(&n)
	return n, err
}

// MarkRead with id 0 marks everything, which is what dismissing the panel does.
func (s *Store) MarkRead(ctx context.Context, id int64) error {
	now := time.Now().Unix()
	if id == 0 {
		_, err := s.w.ExecContext(ctx,
			`UPDATE notification SET read_at = ? WHERE read_at IS NULL`, now)
		return err
	}
	_, err := s.w.ExecContext(ctx,
		`UPDATE notification SET read_at = ? WHERE id = ? AND read_at IS NULL`, now, id)
	return err
}

// DeleteNotification removes one, or all when id is zero. The record of what was
// announced is kept separately, so clearing the list doesn't re-announce them.
func (s *Store) DeleteNotification(ctx context.Context, id int64) (int, error) {
	var (
		res interface{ RowsAffected() (int64, error) }
		err error
	)
	if id == 0 {
		res, err = s.w.ExecContext(ctx, `DELETE FROM notification`)
	} else {
		res, err = s.w.ExecContext(ctx, `DELETE FROM notification WHERE id = ?`, id)
	}
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// AlreadyNotified keeps an episode from being announced again, keyed on the
// episode not the release: many groups sub the same episode, but it's one story.
func (s *Store) AlreadyNotified(ctx context.Context, infoHash string, animeID, episode int) (bool, error) {
	var one int
	err := s.r.QueryRowContext(ctx,
		`SELECT 1 FROM notified_release WHERE info_hash = ?`, infoHash).Scan(&one)
	if err == nil {
		return true, nil
	}
	if animeID == 0 || episode <= 0 {
		return false, nil
	}

	err = s.r.QueryRowContext(ctx,
		`SELECT 1 FROM notified_release WHERE anime_id = ? AND episode = ?`,
		animeID, episode).Scan(&one)
	return err == nil, nil
}

func (s *Store) RecordNotified(ctx context.Context, infoHash string, animeID, episode int) error {
	_, err := s.w.ExecContext(ctx, `
		INSERT INTO notified_release (info_hash, anime_id, episode, created_at)
		VALUES (?,?,?,?) ON CONFLICT DO NOTHING`,
		infoHash, nullableInt(animeID), nullableInt(episode), time.Now().Unix())
	return err
}

type Follow struct {
	AnimeID  int
	Quality  string
	Group    string
	MaxBytes int64
	Titles   []string
	Episodes int
	Progress int

	// The episode the catalogue says is next to air, and when. Zero when the
	// show has finished or nothing has been synced.
	NextEpisode  int
	NextAiringAt int64
}

func (f Follow) Aired(episode int, now int64) bool {
	return aired(episode, f.NextEpisode, f.NextAiringAt, now)
}

// aired reports whether an episode has broadcast: anything at or past the one
// the catalogue is still waiting for has not.
func aired(episode, nextEpisode int, airingAt, now int64) bool {
	if nextEpisode <= 0 || airingAt <= 0 || episode < nextEpisode {
		return true
	}
	return airingAt <= now
}

// Watched shows are those followed plus those set to auto-download (separate
// choices). Grouped by anime, not UNIONed: a show that is both matches twice
// with differing filter columns, which would search every indexer twice per poll.
const followQuery = `
	SELECT anime_id, max(quality), max(group_filter), max(max_bytes),
	       max(episode_count), max(progress),
	       max(next_episode), max(next_airing_at)
	FROM (
		SELECT f.anime_id AS anime_id, coalesce(f.quality,'') AS quality,
		       coalesce(f.group_filter,'') AS group_filter,
		       coalesce(f.max_bytes,0) AS max_bytes,
		       coalesce(a.episode_count,0) AS episode_count,
		       coalesce(e.progress,0) AS progress,
		       coalesce(a.next_episode,0) AS next_episode,
		       coalesce(a.next_airing_at,0) AS next_airing_at
		FROM follow f
		LEFT JOIN anime a ON a.id = f.anime_id
		LEFT JOIN list_entry e ON e.anime_id = f.anime_id
		-- Watching normally stops at the last episode, but an anime whose
		-- episode count is unknown has no last episode and would be searched
		-- for forever. Finishing or abandoning it is the other signal that
		-- there is nothing left to look for.
		WHERE coalesce(e.status, '') NOT IN ('COMPLETED', 'DROPPED')

		UNION ALL

		SELECT p.anime_id, '', '', 0,
		       coalesce(a.episode_count,0), coalesce(e.progress,0),
		       coalesce(a.next_episode,0), coalesce(a.next_airing_at,0)
		FROM anime_pref p
		LEFT JOIN anime a ON a.id = p.anime_id
		LEFT JOIN list_entry e ON e.anime_id = p.anime_id
		WHERE p.key = 'autodownload.enabled' AND p.value = 'true'
		  AND coalesce(e.status, '') NOT IN ('COMPLETED', 'DROPPED')
	)
	GROUP BY anime_id`

func (s *Store) Follows(ctx context.Context) ([]Follow, error) {
	rows, err := s.r.QueryContext(ctx, followQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Follow
	for rows.Next() {
		var f Follow
		if err := rows.Scan(&f.AnimeID, &f.Quality, &f.Group, &f.MaxBytes,
			&f.Episodes, &f.Progress, &f.NextEpisode, &f.NextAiringAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range out {
		out[i].Titles, _ = s.SearchTitles(ctx, out[i].AnimeID)
	}
	return out, nil
}

func (s *Store) SetFollow(ctx context.Context, f Follow, on bool) error {
	if !on {
		return s.Unfollow(ctx, f.AnimeID)
	}
	if err := s.EnsureAnime(ctx, f.AnimeID); err != nil {
		return err
	}
	_, err := s.w.ExecContext(ctx, `
		INSERT INTO follow (anime_id, quality, group_filter, max_bytes, created_at)
		VALUES (?,?,?,?,?)
		ON CONFLICT(anime_id) DO UPDATE SET
		    quality=excluded.quality, group_filter=excluded.group_filter,
		    max_bytes=excluded.max_bytes`,
		f.AnimeID, nullable(f.Quality), nullable(f.Group),
		nullableInt64(f.MaxBytes), time.Now().Unix())
	return err
}

func (s *Store) Unfollow(ctx context.Context, animeID int) error {
	_, err := s.w.ExecContext(ctx, `DELETE FROM follow WHERE anime_id = ?`, animeID)
	return err
}

func deref(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}

func nullableInt64(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}
