package store

import (
	"context"
	"strconv"
	"time"
)

type HistoryEntry struct {
	AnimeID    int      `json:"animeId"`
	Title      string   `json:"title"`
	Romaji     string   `json:"romaji"`
	English    *string  `json:"english,omitempty"`
	Cover      *string  `json:"cover,omitempty"`
	Thumb      *string  `json:"thumb,omitempty"`
	EpKey      string   `json:"epKey"`
	Episode    int      `json:"episode"`
	Position   float64  `json:"position"`
	Duration   *float64 `json:"duration,omitempty"`
	Percent    float64  `json:"percent"`
	Watched    bool     `json:"watched"`
	Dismissed  bool     `json:"dismissed"`
	PlayCount  int      `json:"playCount"`
	LastPlayed int64    `json:"lastPlayed"`
}

const historyQuery = `
SELECT a.id, a.title_romaji, a.title_english, a.cover_url, a.cover_medium,
       p.ep_key, ep.number, p.position_s, p.duration_s,
       p.watched, p.dismissed, p.play_count, p.last_played_at
FROM playback p
JOIN anime a ON a.id = p.anime_id
LEFT JOIN episode ep ON ep.anime_id = p.anime_id AND ep.ep_key = p.ep_key
ORDER BY p.last_played_at DESC, p.rowid DESC
LIMIT ? OFFSET ?`

func (s *Store) History(ctx context.Context, p Paging) (Page[HistoryEntry], error) {
	mode := s.TitleMode(ctx, 0)

	rows, err := s.r.QueryContext(ctx, historyQuery, p.PerPage+1, p.Offset())
	if err != nil {
		return Page[HistoryEntry]{}, err
	}
	defer rows.Close()

	out := []HistoryEntry{}
	for rows.Next() {
		var (
			e                  HistoryEntry
			number             *int
			watched, dismissed int
			duration           *float64
		)
		err := rows.Scan(&e.AnimeID, &e.Romaji, &e.English, &e.Cover, &e.Thumb,
			&e.EpKey, &number, &e.Position, &duration,
			&watched, &dismissed, &e.PlayCount, &e.LastPlayed)
		if err != nil {
			return Page[HistoryEntry]{}, err
		}

		e.Duration = duration
		e.Watched, e.Dismissed = watched == 1, dismissed == 1
		e.Title = PickTitle(mode, e.Romaji, e.English)
		if number != nil {
			e.Episode = *number
		} else {
			e.Episode = episodeFromKey(e.EpKey)
		}
		if duration != nil && *duration > 0 {
			e.Percent = e.Position / *duration * 100
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return Page[HistoryEntry]{}, err
	}

	total, err := s.countRows(ctx, `SELECT count(*) FROM playback`)
	if err != nil {
		return Page[HistoryEntry]{}, err
	}
	return NewPage(out, p, total), nil
}

// Only the playback row goes; tracker progress is a separate record.
func (s *Store) ForgetHistory(ctx context.Context, animeID int, epKey string) (int, error) {
	var (
		res interface{ RowsAffected() (int64, error) }
		err error
	)
	switch {
	case animeID > 0 && epKey != "":
		res, err = s.w.ExecContext(ctx,
			`DELETE FROM playback WHERE anime_id = ? AND ep_key = ?`, animeID, epKey)
	case animeID > 0:
		res, err = s.w.ExecContext(ctx, `DELETE FROM playback WHERE anime_id = ?`, animeID)
	default:
		res, err = s.w.ExecContext(ctx, `DELETE FROM playback`)
	}
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// WatchStats sums what actually played, by window, plus what the list counts
// as done. Days are calendar days on this machine's clock.
type WatchStats struct {
	WeekSeconds  float64 `json:"weekSeconds"`
	MonthSeconds float64 `json:"monthSeconds"`
	TotalSeconds float64 `json:"totalSeconds"`
	Episodes     int     `json:"episodes"`
	Completed    int     `json:"completed"`
	// Days is the last 30 days, oldest first, for a small bar strip.
	Days []DaySeconds `json:"days"`
}

type DaySeconds struct {
	Day     string  `json:"day"`
	Seconds float64 `json:"seconds"`
}

func (s *Store) WatchStats(ctx context.Context, now time.Time) (WatchStats, error) {
	var out WatchStats
	week := now.AddDate(0, 0, -6).Format("2006-01-02")
	month := now.AddDate(0, 0, -29).Format("2006-01-02")
	err := s.r.QueryRowContext(ctx, `
		SELECT coalesce(sum(CASE WHEN day >= ?1 THEN seconds END), 0),
		       coalesce(sum(CASE WHEN day >= ?2 THEN seconds END), 0),
		       coalesce(sum(seconds), 0),
		       (SELECT count(*) FROM playback WHERE watched = 1),
		       (SELECT count(*) FROM list_entry WHERE status = 'COMPLETED')
		FROM watch_time`, week, month).
		Scan(&out.WeekSeconds, &out.MonthSeconds, &out.TotalSeconds, &out.Episodes, &out.Completed)
	if err != nil {
		return out, err
	}

	rows, err := s.r.QueryContext(ctx, `SELECT day, seconds FROM watch_time WHERE day >= ?`, month)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	by := map[string]float64{}
	for rows.Next() {
		var day string
		var secs float64
		if err := rows.Scan(&day, &secs); err != nil {
			return out, err
		}
		by[day] = secs
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	out.Days = make([]DaySeconds, 0, 30)
	for i := 29; i >= 0; i-- {
		day := now.AddDate(0, 0, -i).Format("2006-01-02")
		out.Days = append(out.Days, DaySeconds{Day: day, Seconds: by[day]})
	}
	return out, nil
}

func episodeFromKey(key string) int {
	n, err := strconv.Atoi(key)
	if err != nil {
		return 0
	}
	return n
}
