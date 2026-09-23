package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// AnimeRecord is the saved row, for when AniList cannot give a current one.
// A stub (synced_at 0) holds no record worth showing.
func (s *Store) AnimeRecord(ctx context.Context, id int) (Anime, time.Time, bool, error) {
	var (
		a      Anime
		synced int64
	)
	err := s.r.QueryRowContext(ctx, `
		SELECT id, mal_id, title_romaji, title_english, title_native, coalesce(synonyms, '[]'),
		       format, status, episode_count, duration, season, season_year,
		       country, coalesce(is_adult, 0), description, cover_url, cover_color, cover_medium, banner_url,
		       average_score, mean_score, popularity, favourites,
		       coalesce(genres, '[]'), coalesce(studios, '[]'), coalesce(tags, '[]'), coalesce(links, '[]'),
		       source, coalesce(start_date, ''), coalesce(end_date, ''),
		       trailer_id, trailer_site, next_episode, next_airing_at, synced_at
		FROM anime WHERE id = ? AND synced_at > 0`, id).Scan(
		&a.ID, &a.MalID, &a.Romaji, &a.English, &a.Native, &a.Synonyms,
		&a.Format, &a.Status, &a.Episodes, &a.Duration, &a.Season, &a.SeasonYear,
		&a.Country, &a.IsAdult, &a.Description, &a.Cover, &a.CoverColor, &a.CoverMedium, &a.Banner,
		&a.AverageScore, &a.MeanScore, &a.Popularity, &a.Favourites,
		&a.Genres, &a.Studios, &a.Tags, &a.Links,
		&a.Source, &a.StartDate, &a.EndDate,
		&a.TrailerID, &a.TrailerSite, &a.NextEpisode, &a.NextAiringAt, &synced)
	if errors.Is(err, sql.ErrNoRows) {
		return Anime{}, time.Time{}, false, nil
	}
	if err != nil {
		return Anime{}, time.Time{}, false, err
	}
	return a, time.Unix(synced, 0), true, nil
}

// How long a saved airing show may go unchecked. Past its next broadcast it is
// due at once: the episode count, the next date and in time the status change.
const (
	airingMaxAge   = 6 * time.Hour
	upcomingMaxAge = 24 * time.Hour
)

// StaleAiring lists saved shows still airing or announced whose row is due a
// refresh, those whose broadcast has passed first.
func (s *Store) StaleAiring(ctx context.Context, now time.Time, limit int) ([]int, error) {
	rows, err := s.r.QueryContext(ctx, `
		SELECT id FROM anime
		WHERE id > 0 AND synced_at > 0
		  AND status IN ('RELEASING', 'NOT_YET_RELEASED')
		  AND ((next_airing_at IS NOT NULL AND next_airing_at <= ?1 AND synced_at < next_airing_at)
		       OR synced_at < ?1 - CASE status WHEN 'RELEASING' THEN ?2 ELSE ?3 END)
		ORDER BY (next_airing_at IS NOT NULL AND next_airing_at <= ?1) DESC, synced_at
		LIMIT ?4`,
		now.Unix(), int64(airingMaxAge.Seconds()), int64(upcomingMaxAge.Seconds()), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
