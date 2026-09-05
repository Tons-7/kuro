package store

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"
)

// QueuedDownload is one episode waiting its turn. The queue is deliberately
// serial: parallel torrents share one connection and each finishes later.
type QueuedDownload struct {
	AnimeID int     `json:"animeId"`
	EpKey   string  `json:"epKey"`
	Episode int     `json:"episode"`
	Season  int     `json:"season"`
	State   string  `json:"state"`
	Error   *string `json:"error,omitempty"`
	Title   string  `json:"title,omitempty"`
	Cover   *string `json:"cover,omitempty"`
}

// Enqueue adds episodes to the back of the queue. An already-queued episode keeps
// its place (pressing download twice is harmless); one already on disk is dropped
// by the worker, not here.
func (s *Store) Enqueue(ctx context.Context, animeID, season int, episodes []int) (int, error) {
	if animeID == 0 || len(episodes) == 0 {
		return 0, nil
	}
	if err := s.EnsureAnime(ctx, animeID); err != nil {
		return 0, err
	}

	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO download_queue (anime_id, ep_key, episode, season, state, queued_at)
		VALUES (?,?,?,?,'pending',?)
		ON CONFLICT(anime_id, ep_key) DO UPDATE SET
		    state = CASE WHEN download_queue.state IN ('failed','done')
		                 THEN 'pending' ELSE download_queue.state END,
		    error = NULL`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	now := time.Now().Unix()
	var added int
	for _, episode := range episodes {
		if episode <= 0 {
			continue
		}
		if _, err := stmt.ExecContext(ctx, animeID, strconv.Itoa(episode), episode, season, now); err != nil {
			return 0, err
		}
		added++
	}
	return added, tx.Commit()
}

// NextQueued claims the oldest pending episode and marks it active, so a
// restart mid-download does not leave a row that nothing will ever pick up.
func (s *Store) NextQueued(ctx context.Context) (QueuedDownload, bool, error) {
	var q QueuedDownload
	err := s.r.QueryRowContext(ctx, `
		SELECT anime_id, ep_key, episode, season
		FROM download_queue
		WHERE state = 'pending'
		ORDER BY queued_at, episode
		LIMIT 1`).Scan(&q.AnimeID, &q.EpKey, &q.Episode, &q.Season)
	if errors.Is(err, sql.ErrNoRows) {
		return QueuedDownload{}, false, nil
	}
	if err != nil {
		return QueuedDownload{}, false, err
	}

	if _, err := s.w.ExecContext(ctx, `
		UPDATE download_queue SET state = 'active', started_at = ?
		WHERE anime_id = ? AND ep_key = ?`,
		time.Now().Unix(), q.AnimeID, q.EpKey); err != nil {
		return QueuedDownload{}, false, err
	}
	q.State = "active"
	return q, true, nil
}

// FinishQueued records the outcome. A failure is kept rather than deleted: a
// row that vanishes silently is indistinguishable from one that worked.
func (s *Store) FinishQueued(ctx context.Context, animeID int, epKey string, cause error) error {
	if cause == nil {
		_, err := s.w.ExecContext(ctx,
			`DELETE FROM download_queue WHERE anime_id = ? AND ep_key = ?`, animeID, epKey)
		return err
	}
	_, err := s.w.ExecContext(ctx, `
		UPDATE download_queue SET state = 'failed', error = ?
		WHERE anime_id = ? AND ep_key = ?`, cause.Error(), animeID, epKey)
	return err
}

// RequeueActive puts one interrupted episode back in line, reporting whether a
// row was still there. A cancel deletes the row first, so false tells them apart.
func (s *Store) RequeueActive(ctx context.Context, animeID int, epKey string) (bool, error) {
	res, err := s.w.ExecContext(ctx, `
		UPDATE download_queue SET state = 'pending', started_at = NULL
		WHERE state = 'active' AND anime_id = ? AND ep_key = ?`, animeID, epKey)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ResetActive returns anything left mid-flight to the queue, for startup.
func (s *Store) ResetActive(ctx context.Context) error {
	_, err := s.w.ExecContext(ctx,
		`UPDATE download_queue SET state = 'pending', started_at = NULL WHERE state = 'active'`)
	return err
}

func (s *Store) QueuedDownloads(ctx context.Context) ([]QueuedDownload, error) {
	mode := s.TitleMode(ctx, 0)

	rows, err := s.r.QueryContext(ctx, `
		SELECT q.anime_id, q.ep_key, q.episode, q.season, q.state, q.error,
		       a.title_romaji, a.title_english, a.cover_url
		FROM download_queue q
		LEFT JOIN anime a ON a.id = q.anime_id
		ORDER BY q.state = 'active' DESC, q.queued_at, q.episode
		LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []QueuedDownload{}
	for rows.Next() {
		var q QueuedDownload
		var romaji, english *string
		if err := rows.Scan(&q.AnimeID, &q.EpKey, &q.Episode, &q.Season, &q.State, &q.Error,
			&romaji, &english, &q.Cover); err != nil {
			return nil, err
		}
		if romaji != nil {
			q.Title = PickTitle(mode, *romaji, english)
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

// Dequeue drops queued episodes in any state, including the one being fetched
// (a cancel must actually stop it) and failed ones. Bytes already on disk stay;
// removing those is a separate choice.
func (s *Store) Dequeue(ctx context.Context, animeID int, epKey string) (int, error) {
	query := `DELETE FROM download_queue WHERE anime_id = ?`
	args := []any{animeID}
	if epKey != "" {
		query += ` AND ep_key = ?`
		args = append(args, epKey)
	}

	res, err := s.w.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (s *Store) ClearQueue(ctx context.Context) (int, error) {
	res, err := s.w.ExecContext(ctx, `DELETE FROM download_queue WHERE state != 'active'`)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}
