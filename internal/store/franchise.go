package store

import (
	"context"
	"sort"
)

type Relation struct {
	AnimeID   int
	RelatedID int
	Kind      string
}

type Season struct {
	ID       int     `json:"id"`
	Ordinal  int     `json:"ordinal"`
	Romaji   string  `json:"romaji"`
	English  *string `json:"english"`
	Cover    *string `json:"cover"`
	Episodes *int    `json:"episodes"`
	Year     *int    `json:"year"`
	Format   *string `json:"format"`
	Status   *string `json:"status"`
	// ListStatus is what the user tagged it, which is a different question from
	// Status: a finished show can be unwatched, and a watched one still airing.
	ListStatus *string `json:"listStatus,omitempty"`
	Progress   int     `json:"progress"`
	OnList     bool    `json:"onList"`
}

type Franchise struct {
	RootID  int      `json:"rootId"`
	Seasons []Season `json:"seasons"`
}

// HasRelations reports whether the graph has been walked for this anime. An
// entry placed in a franchise of its own counts: a standalone show has no
// edges, and retrying would cost a request every time it is opened.
func (s *Store) HasRelations(ctx context.Context, animeID int) (bool, error) {
	var count int
	err := s.r.QueryRowContext(ctx, `
		SELECT (SELECT count(*) FROM relation WHERE anime_id = ? OR related_id = ?)
		     + (SELECT count(*) FROM franchise WHERE anime_id = ?)`,
		animeID, animeID, animeID).Scan(&count)
	return count > 0, err
}

// MarkNoRelations records a show as looked up and standalone. A self-edge, not
// a franchise row: that table is rebuilt from scratch on any other fetch.
func (s *Store) MarkNoRelations(ctx context.Context, animeID int) error {
	if animeID == 0 {
		return nil
	}
	_, err := s.w.ExecContext(ctx, `
		INSERT INTO relation (anime_id, related_id, kind) VALUES (?, ?, 'NONE')
		ON CONFLICT(anime_id, related_id, kind) DO NOTHING`, animeID, animeID)
	return err
}

func (s *Store) SaveRelations(ctx context.Context, rels []Relation) error {
	if len(rels) == 0 {
		return nil
	}
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO relation (anime_id, related_id, kind) VALUES (?,?,?)
		 ON CONFLICT DO NOTHING`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, r := range rels {
		if r.AnimeID == 0 || r.RelatedID == 0 {
			continue
		}
		if _, err := stmt.ExecContext(ctx, r.AnimeID, r.RelatedID, r.Kind); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// RebuildFranchises walks PREQUEL/SEQUEL edges into connected components and
// numbers each member in broadcast order. Side stories and spin-offs are
// deliberately excluded: they are related, but they are not seasons.
func (s *Store) RebuildFranchises(ctx context.Context) (int, error) {
	rows, err := s.r.QueryContext(ctx,
		`SELECT anime_id, related_id FROM relation WHERE kind IN ('PREQUEL','SEQUEL')`)
	if err != nil {
		return 0, err
	}

	adj := map[int][]int{}
	for rows.Next() {
		var a, b int
		if err := rows.Scan(&a, &b); err != nil {
			rows.Close()
			return 0, err
		}
		adj[a] = append(adj[a], b)
		adj[b] = append(adj[b], a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	dates, err := s.startDates(ctx)
	if err != nil {
		return 0, err
	}

	seen := map[int]bool{}
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM franchise`); err != nil {
		return 0, err
	}
	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO franchise (anime_id, root_id, ordinal) VALUES (?,?,?)`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	var groups int
	for id := range adj {
		if seen[id] {
			continue
		}

		members := component(id, adj, seen)
		// Broadcast order, not graph order: the edges give no direction, and a
		// missing date must not reorder the rest.
		sort.Slice(members, func(i, j int) bool {
			di, dj := dates[members[i]], dates[members[j]]
			if di != dj {
				return di < dj
			}
			return members[i] < members[j]
		})

		root := members[0]
		for ordinal, member := range members {
			if _, err := stmt.ExecContext(ctx, member, root, ordinal+1); err != nil {
				return 0, err
			}
		}
		groups++
	}

	return groups, tx.Commit()
}

func component(start int, adj map[int][]int, seen map[int]bool) []int {
	var out []int
	stack := []int{start}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
		stack = append(stack, adj[id]...)
	}
	return out
}

// A missing date sorts last rather than first, so an unaired sequel does not
// become season one.
func (s *Store) startDates(ctx context.Context) (map[int]int, error) {
	rows, err := s.r.QueryContext(ctx, `
		SELECT anime_id,
		       coalesce(nullif(replace(coalesce(start_date,''), '-', ''), ''), '99999999')
		FROM (
		    SELECT id AS anime_id, start_date FROM anime
		    UNION ALL
		    SELECT anime_id, NULL FROM corpus_anime
		    WHERE anime_id NOT IN (SELECT id FROM anime)
		)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[int]int{}
	for rows.Next() {
		var id int
		var date string
		if err := rows.Scan(&id, &date); err != nil {
			return nil, err
		}
		n := 99999999
		if v, err := atoiSafe(date); err == nil && v > 0 {
			n = v
		}
		out[id] = n
	}
	return out, rows.Err()
}

const franchiseQuery = `
SELECT f.anime_id, f.ordinal,
       coalesce(a.title_romaji, ''), a.title_english, a.cover_url,
       coalesce(a.episode_count, c.episodes), coalesce(a.season_year, c.year),
       a.format, a.status, e.status,
       coalesce(e.progress, 0), e.id IS NOT NULL
FROM franchise f
LEFT JOIN anime a       ON a.id = f.anime_id
LEFT JOIN corpus_anime c ON c.anime_id = f.anime_id
LEFT JOIN list_entry e   ON e.anime_id = f.anime_id
WHERE f.root_id = (SELECT root_id FROM franchise WHERE anime_id = ?)
ORDER BY f.ordinal`

// Franchise returns every season of the show an id belongs to, in broadcast
// order. An anime with no relations is a franchise of one.
func (s *Store) Franchise(ctx context.Context, animeID int) (Franchise, error) {
	rows, err := s.r.QueryContext(ctx, franchiseQuery, animeID)
	if err != nil {
		return Franchise{}, err
	}
	defer rows.Close()

	out := Franchise{Seasons: []Season{}}
	for rows.Next() {
		var s Season
		if err := rows.Scan(&s.ID, &s.Ordinal, &s.Romaji, &s.English, &s.Cover,
			&s.Episodes, &s.Year, &s.Format, &s.Status, &s.ListStatus,
			&s.Progress, &s.OnList); err != nil {
			return Franchise{}, err
		}
		out.Seasons = append(out.Seasons, s)
	}
	if err := rows.Err(); err != nil {
		return Franchise{}, err
	}

	if len(out.Seasons) == 0 {
		return s.soloFranchise(ctx, animeID)
	}
	out.RootID = out.Seasons[0].ID
	return out, nil
}

func (s *Store) soloFranchise(ctx context.Context, animeID int) (Franchise, error) {
	var season Season
	err := s.r.QueryRowContext(ctx, `
		SELECT a.id, coalesce(a.title_romaji, ''), a.title_english, a.cover_url,
		       a.episode_count, a.season_year, a.format, a.status, e.status,
		       coalesce(e.progress, 0), e.id IS NOT NULL
		FROM anime a
		LEFT JOIN list_entry e ON e.anime_id = a.id
		WHERE a.id = ?`, animeID).Scan(
		&season.ID, &season.Romaji, &season.English, &season.Cover,
		&season.Episodes, &season.Year, &season.Format, &season.Status,
		&season.ListStatus, &season.Progress, &season.OnList)
	if err != nil {
		return Franchise{RootID: animeID, Seasons: []Season{}}, nil
	}
	season.Ordinal = 1
	return Franchise{RootID: animeID, Seasons: []Season{season}}, nil
}

// EnglishTitle returns the show's English name on its own: release groups post
// the same episode under both romaji and English, so searching only the romaji
// misses every English-named release.
func (s *Store) EnglishTitle(ctx context.Context, animeID int) (string, error) {
	var title *string
	err := s.r.QueryRowContext(ctx,
		`SELECT title_english FROM anime WHERE id = ?`, animeID).Scan(&title)
	if err != nil || title == nil {
		// A missing row is normal for anime that were never imported.
		return "", nil
	}
	return *title, nil
}

func (s *Store) SearchTitles(ctx context.Context, animeID int) ([]string, error) {
	rows, err := s.r.QueryContext(ctx, `
		SELECT text FROM (
		    SELECT title_romaji AS text, 0 AS rank FROM anime WHERE id = ?1 AND title_romaji <> ''
		    UNION
		    SELECT title_english, 1 FROM anime WHERE id = ?1 AND title_english IS NOT NULL
		    UNION
		    SELECT t.text, CASE t.kind WHEN 'primary' THEN 0 WHEN 'official' THEN 1 ELSE 2 END
		    FROM title t WHERE t.anime_id = ?1
		)
		WHERE text IS NOT NULL AND text <> ''
		ORDER BY rank, length(text)
		LIMIT 12`, animeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) EpisodeCount(ctx context.Context, animeID int) (int, error) {
	var count *int
	err := s.r.QueryRowContext(ctx, `
		SELECT coalesce(a.episode_count, c.episodes)
		FROM corpus_anime c
		LEFT JOIN anime a ON a.id = c.anime_id
		WHERE c.anime_id = ?
		UNION ALL
		SELECT episode_count FROM anime WHERE id = ?
		LIMIT 1`, animeID, animeID).Scan(&count)
	if err != nil || count == nil {
		return 0, nil
	}
	return *count, nil
}

func atoiSafe(s string) (int, error) {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, errNotNumber
		}
		n = n*10 + int(r-'0')
	}
	return n, nil
}

var errNotNumber = errorString("not a number")

type errorString string

func (e errorString) Error() string { return string(e) }
