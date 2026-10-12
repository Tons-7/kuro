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

// DeleteNotification removes one, or all when id is zero; what was announced is recorded apart from the list.
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

// AlreadyNotified keys on the episode, not the release: many groups sub the same episode, one story.
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
	Titles   []string
	Episodes int
	Progress int

	// The episode the catalogue says airs next, and when; zero once the show has finished or before a sync.
	NextEpisode  int
	NextAiringAt int64
	EndDate      string // YYYY-MM-DD, empty while airing or unknown
}

func (f Follow) Aired(episode int, now int64) bool {
	return aired(episode, f.NextEpisode, f.NextAiringAt, now)
}

// Latest is the episode that aired last, or 0: the one before the next (the next itself once its time
// has passed), or the last of a show that ended this week.
func (f Follow) Latest(now int64) int {
	switch {
	case f.NextEpisode > 0 && f.NextAiringAt > 0 && f.NextAiringAt <= now:
		return f.NextEpisode
	case f.NextEpisode > 0:
		return f.NextEpisode - 1
	}
	if ended, err := time.Parse("2006-01-02", f.EndDate); err == nil && now-ended.Unix() < 7*24*3600 {
		return f.Episodes
	}
	return 0
}

// aired: only the next episode's time is known; anything past it airs later still, however stale the row.
func aired(episode, nextEpisode int, airingAt, now int64) bool {
	if nextEpisode <= 0 || airingAt <= 0 || episode < nextEpisode {
		return true
	}
	if episode > nextEpisode {
		return false
	}
	return airingAt <= now
}

// EpisodeAired: has the episode broadcast, and when does the next; no schedule counts as aired.
func (s *Store) EpisodeAired(ctx context.Context, animeID, episode int) (bool, int64) {
	var next, at *int
	err := s.r.QueryRowContext(ctx,
		`SELECT next_episode, next_airing_at FROM anime WHERE id = ?`, animeID).Scan(&next, &at)
	if err != nil || next == nil || at == nil {
		return true, 0
	}
	return aired(episode, *next, int64(*at), time.Now().Unix()), int64(*at)
}

// Watched: followed, auto-download, or listed as watching; grouped, so a show that is several is searched once.
const followQuery = `
	SELECT anime_id, max(episode_count), max(progress),
	       max(next_episode), max(next_airing_at), max(end_date)
	FROM (
		SELECT f.anime_id AS anime_id,
		       coalesce(a.episode_count,0) AS episode_count,
		       coalesce(e.progress,0) AS progress,
		       coalesce(a.next_episode,0) AS next_episode,
		       coalesce(a.next_airing_at,0) AS next_airing_at,
		       coalesce(a.end_date,'') AS end_date
		FROM follow f
		LEFT JOIN anime a ON a.id = f.anime_id
		LEFT JOIN list_entry e ON e.anime_id = f.anime_id
		-- A show with no known episode count has no last episode to stop at; finishing or dropping it does.
		WHERE coalesce(e.status, '') NOT IN ('COMPLETED', 'DROPPED')

		UNION ALL

		SELECT p.anime_id,
		       coalesce(a.episode_count,0), coalesce(e.progress,0),
		       coalesce(a.next_episode,0), coalesce(a.next_airing_at,0), coalesce(a.end_date,'')
		FROM anime_pref p
		LEFT JOIN anime a ON a.id = p.anime_id
		LEFT JOIN list_entry e ON e.anime_id = p.anime_id
		WHERE p.key = 'autodownload.enabled' AND p.value = 'true'
		  AND coalesce(e.status, '') NOT IN ('COMPLETED', 'DROPPED')

		UNION ALL

		-- A watching status synced from the tracker followed nothing, so nobody heard; muted on its page stays quiet.
		SELECT e.anime_id,
		       coalesce(a.episode_count,0), coalesce(e.progress,0),
		       coalesce(a.next_episode,0), coalesce(a.next_airing_at,0), coalesce(a.end_date,'')
		FROM list_entry e
		LEFT JOIN anime a ON a.id = e.anime_id
		WHERE e.status IN ('CURRENT', 'REPEATING')
		  AND NOT EXISTS (SELECT 1 FROM anime_pref p
		                  WHERE p.anime_id = e.anime_id AND p.key = 'notify.enabled' AND p.value = 'false')
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
		if err := rows.Scan(&f.AnimeID, &f.Episodes, &f.Progress, &f.NextEpisode, &f.NextAiringAt, &f.EndDate); err != nil {
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

func (s *Store) SetFollow(ctx context.Context, animeID int, on bool) error {
	if !on {
		return s.Unfollow(ctx, animeID)
	}
	if err := s.EnsureAnime(ctx, animeID); err != nil {
		return err
	}
	_, err := s.w.ExecContext(ctx, `
		INSERT INTO follow (anime_id, created_at) VALUES (?,?)
		ON CONFLICT(anime_id) DO NOTHING`,
		animeID, time.Now().Unix())
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
