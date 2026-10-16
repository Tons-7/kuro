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
ORDER BY e.anime_id IS NULL, bm25(anime_fts, 10.0, 8.0, 4.0, 2.0), a.popularity DESC
LIMIT ?`

// The list's shows lead here too, or popular ones push them out of the header box's eight.
const searchLike = `
SELECT ` + searchColumns + `
FROM anime a
LEFT JOIN list_entry e ON e.anime_id = a.id
WHERE a.title_romaji LIKE ?1 ESCAPE '\' OR a.title_english LIKE ?1 ESCAPE '\'
   OR a.title_native LIKE ?1 ESCAPE '\' OR a.synonyms LIKE ?1 ESCAPE '\'
ORDER BY e.anime_id IS NULL, a.popularity DESC
LIMIT ?2`

// SearchLocal uses FTS, or LIKE for CJK and queries under three characters, which FTS5 can't match.
func (s *Store) SearchLocal(ctx context.Context, q string, limit int) ([]LibraryItem, error) {
	q = plainQuotes.Replace(strings.TrimSpace(q))
	if q == "" {
		return []LibraryItem{}, nil
	}
	if limit <= 0 || limit > 100 {
		limit = 30
	}

	if needsSubstringMatch(q) {
		return s.query(ctx, searchLike, "%"+escapeLike(q)+"%", limit)
	}
	return s.query(ctx, searchFTS, ftsPrefix(q), limit)
}

// Phone keyboards type curly quotes; titles are stored with straight ones.
var plainQuotes = strings.NewReplacer("‘", "'", "’", "'", "“", `"`, "”", `"`)

// escapeLike makes the text literal inside a LIKE pattern, for use with ESCAPE '\'.
func escapeLike(q string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q)
}

func needsSubstringMatch(q string) bool {
	if len([]rune(q)) < 3 {
		return true
	}
	for _, r := range q {
		if unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul, unicode.Thai) {
			return true
		}
	}
	return false
}

// A trailing * prefix-matches the last token while typing; quoting keeps FTS5 operators out.
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
