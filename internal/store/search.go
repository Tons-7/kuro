package store

import (
	"context"
	"strings"
	"unicode"
)

const searchColumns = `a.id, a.title_romaji, a.title_english, a.cover_url, a.episode_count,
       e.status, COALESCE(e.progress, 0), COALESCE(e.score, 0), a.next_episode, a.next_airing_at`

const searchFTS = `
SELECT ` + searchColumns + `
FROM anime_fts f
JOIN anime a ON a.id = f.rowid
LEFT JOIN list_entry e ON e.anime_id = a.id
WHERE anime_fts MATCH ?
ORDER BY bm25(anime_fts, 10.0, 8.0, 4.0, 2.0)
LIMIT ?`

const searchLike = `
SELECT ` + searchColumns + `
FROM anime a
LEFT JOIN list_entry e ON e.anime_id = a.id
WHERE a.title_romaji LIKE ?1 OR a.title_english LIKE ?1
   OR a.title_native LIKE ?1 OR a.synonyms LIKE ?1
ORDER BY a.popularity DESC
LIMIT ?2`

// SearchLocal queries the library only; AniList is a separate, slower path.
// FTS5 can't segment CJK or match under three characters, so anything non-latin
// or shorter than three characters falls back to LIKE.
func (s *Store) SearchLocal(ctx context.Context, q string, limit int) ([]LibraryItem, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return []LibraryItem{}, nil
	}
	if limit <= 0 || limit > 100 {
		limit = 30
	}

	if needsSubstringMatch(q) {
		return s.query(ctx, searchLike, "%"+q+"%", limit)
	}
	return s.query(ctx, searchFTS, ftsPrefix(q), limit)
}

func needsSubstringMatch(q string) bool {
	if len([]rune(q)) < 3 {
		return true
	}
	for _, r := range q {
		if r > unicode.MaxASCII {
			return true
		}
	}
	return false
}

// Trailing * makes the last token a prefix match so results appear while
// typing. Quoting each token keeps FTS5 operators out of user input.
func ftsPrefix(q string) string {
	fields := strings.Fields(q)
	for i, f := range fields {
		f = `"` + strings.ReplaceAll(f, `"`, `""`) + `"`
		if i == len(fields)-1 {
			f += "*"
		}
		fields[i] = f
	}
	return strings.Join(fields, " ")
}

func (s *Store) query(ctx context.Context, sql string, args ...any) ([]LibraryItem, error) {
	rows, err := s.r.QueryContext(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []LibraryItem{}
	for rows.Next() {
		var it LibraryItem
		if err := rows.Scan(&it.ID, &it.Romaji, &it.English, &it.Cover, &it.Episodes,
			&it.Status, &it.Progress, &it.Score, &it.NextEpisode, &it.NextAiringAt); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}
