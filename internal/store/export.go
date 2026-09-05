package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ExportEntry is one row of a portable library file: what a tracker holds plus
// the favourite and note kuro keeps on its own.
type ExportEntry struct {
	AnimeID     int    `json:"animeId"`
	MalID       *int   `json:"malId,omitempty"`
	Title       string `json:"title"`
	English     string `json:"english,omitempty"`
	Episodes    *int   `json:"episodes,omitempty"`
	Status      string `json:"status,omitempty"`
	Progress    int    `json:"progress"`
	Score       int    `json:"score,omitempty"`
	Repeat      int    `json:"repeat,omitempty"`
	StartedAt   string `json:"startedAt,omitempty"`
	CompletedAt string `json:"completedAt,omitempty"`
	Favourite   bool   `json:"favourite,omitempty"`
	Note        string `json:"note,omitempty"`
}

const exportQuery = `
SELECT a.id, a.mal_id, a.title_romaji, coalesce(a.title_english, ''),
       a.episode_count,
       coalesce(e.status, ''), coalesce(e.progress, 0), coalesce(e.score, 0),
       coalesce(e.repeat_count, 0),
       coalesce(e.started_at, ''), coalesce(e.completed_at, ''),
       coalesce(u.favourite, 0), coalesce(u.note, '')
FROM anime a
LEFT JOIN list_entry e ON e.anime_id = a.id
LEFT JOIN user_anime u ON u.anime_id = a.id
WHERE e.anime_id IS NOT NULL OR u.favourite = 1
ORDER BY a.title_romaji`

func (s *Store) ExportLibrary(ctx context.Context) ([]ExportEntry, error) {
	rows, err := s.r.QueryContext(ctx, exportQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []ExportEntry{}
	for rows.Next() {
		var e ExportEntry
		var favourite int
		if err := rows.Scan(&e.AnimeID, &e.MalID, &e.Title, &e.English, &e.Episodes,
			&e.Status, &e.Progress, &e.Score, &e.Repeat,
			&e.StartedAt, &e.CompletedAt, &favourite, &e.Note); err != nil {
			return nil, err
		}
		e.Favourite = favourite == 1
		out = append(out, e)
	}
	return out, rows.Err()
}

// AnimeIDByMAL maps a MyAnimeList id to the catalogue's, from what has been
// imported or the offline corpus.
func (s *Store) AnimeIDByMAL(ctx context.Context, malID int) (int, bool, error) {
	var id int
	err := s.r.QueryRowContext(ctx, `
		SELECT id FROM anime WHERE mal_id = ?
		UNION ALL
		SELECT anime_id FROM corpus_anime WHERE mal_id = ?
		LIMIT 1`, malID, malID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return id, true, nil
}

type ImportReport struct {
	Entries    int `json:"entries"`
	Favourites int `json:"favourites"`
	Skipped    int `json:"skipped"`
}

// ImportEntries merges a library file: progress only moves forward, and what
// is applied is queued for the trackers as a local edit.
func (s *Store) ImportEntries(ctx context.Context, entries []ExportEntry) (ImportReport, error) {
	var rep ImportReport
	now := time.Now().Unix()

	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return rep, err
	}
	defer tx.Rollback()

	for _, e := range entries {
		if e.AnimeID <= 0 {
			rep.Skipped++
			continue
		}
		if err := ensureAnime(ctx, tx, e.AnimeID); err != nil {
			return rep, err
		}

		if _, err := tx.ExecContext(ctx, `
			INSERT INTO list_entry (id, anime_id, status, progress, score, repeat_count,
			                        started_at, completed_at,
			                        remote_updated_at, local_updated_at, dirty)
			VALUES ((SELECT min(coalesce(min(id), 0), 0) - 1 FROM list_entry),
			        ?, ?, ?, ?, ?, ?, ?, 0, ?, 1)
			ON CONFLICT(anime_id) DO UPDATE SET
			    progress = max(list_entry.progress, excluded.progress),
			    status = coalesce(excluded.status, list_entry.status),
			    score = CASE WHEN excluded.score > 0 THEN excluded.score ELSE list_entry.score END,
			    repeat_count = max(list_entry.repeat_count, excluded.repeat_count),
			    started_at = coalesce(list_entry.started_at, excluded.started_at),
			    completed_at = coalesce(list_entry.completed_at, excluded.completed_at),
			    local_updated_at = excluded.local_updated_at,
			    dirty = 1`,
			e.AnimeID, nullable(e.Status), e.Progress, e.Score, e.Repeat,
			nullable(e.StartedAt), nullable(e.CompletedAt), now); err != nil {
			return rep, err
		}
		rep.Entries++

		if !e.Favourite && e.Note == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO user_anime (anime_id, favourite, note, updated_at,
			                        favourite_synced, favourite_dirty)
			VALUES (?, ?, ?, ?, 0, ?)
			ON CONFLICT(anime_id) DO UPDATE SET
			    favourite = max(user_anime.favourite, excluded.favourite),
			    note = coalesce(excluded.note, user_anime.note),
			    updated_at = excluded.updated_at,
			    favourite_dirty = CASE
			        WHEN max(user_anime.favourite, excluded.favourite) <> user_anime.favourite_synced
			        THEN 1 ELSE user_anime.favourite_dirty END`,
			e.AnimeID, boolInt(e.Favourite), nullable(e.Note), now, boolInt(e.Favourite)); err != nil {
			return rep, err
		}
		if e.Favourite {
			rep.Favourites++
		}
	}
	if err := tx.Commit(); err != nil {
		return rep, err
	}
	return rep, nil
}
