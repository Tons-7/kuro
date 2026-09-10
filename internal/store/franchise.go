package store

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"time"
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

// RelatedEntry is a film, OVA, special or spin-off of the same franchise.
type RelatedEntry struct {
	Season
	// Kind is AniList's edge type: SIDE_STORY, SPIN_OFF, SUMMARY, ALTERNATIVE…
	Kind string `json:"kind"`
}

// Anything the franchise links to that is not part of the season chain, and is
// not itself a season of it. Newest last: films are listed as they came out.
const relatedQuery = `
WITH members AS (
    SELECT anime_id FROM franchise
    WHERE root_id = (SELECT root_id FROM franchise WHERE anime_id = ?)
    UNION SELECT ?
),
-- Both ways round: edges are stored from whichever entry was walked, so a
-- film opened on its own would otherwise list nothing.
linked AS (
    SELECT r.related_id AS id, r.kind AS kind
    FROM relation r JOIN members m ON m.anime_id = r.anime_id
    UNION ALL
    SELECT r.anime_id AS id,
           CASE r.kind WHEN 'SIDE_STORY' THEN 'PARENT'
                       WHEN 'SPIN_OFF'   THEN 'PARENT'
                       WHEN 'PARENT'     THEN 'SIDE_STORY'
                       ELSE r.kind END AS kind
    FROM relation r JOIN members m ON m.anime_id = r.related_id
),
edges AS (
    SELECT id, min(kind) AS kind
    FROM linked
    WHERE kind NOT IN ('PREQUEL','SEQUEL')
      AND id NOT IN (SELECT anime_id FROM members)
      AND id NOT IN (SELECT anime_id FROM dead_anime)
      AND id <> ?
    GROUP BY id
)
SELECT e.id, e.kind,
       coalesce(a.title_romaji, ''), a.title_english, a.cover_url,
       coalesce(a.episode_count, c.episodes),
       -- OVAs and films often carry a start date but no season.
       coalesce(a.season_year, c.year, cast(substr(a.start_date, 1, 4) AS INTEGER)),
       a.format, a.status, l.status, coalesce(l.progress, 0), l.id IS NOT NULL
FROM edges e
LEFT JOIN anime a        ON a.id = e.id
LEFT JOIN corpus_anime c ON c.anime_id = e.id
LEFT JOIN list_entry l   ON l.anime_id = e.id
ORDER BY coalesce(a.season_year, c.year, cast(substr(a.start_date, 1, 4) AS INTEGER), 9999), a.title_romaji
LIMIT 200`

// Kinds that are seasons of the same show. Everything else is related material
// the page lists separately; a spin-off wedged into the chain breaks numbering.
var SeasonKinds = map[string]bool{"PREQUEL": true, "SEQUEL": true}

// MarkRelationsFetched records when a franchise was walked, so it can be walked
// again once a new sequel or film has had time to appear.
func (s *Store) MarkRelationsFetched(ctx context.Context, ids []int) error {
	if len(ids) == 0 {
		return nil
	}
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO relation_fetch (anime_id, fetched_at) VALUES (?, unixepoch())
		ON CONFLICT(anime_id) DO UPDATE SET fetched_at = excluded.fetched_at`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, id := range ids {
		if _, err := stmt.ExecContext(ctx, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// RelationsStale reports whether the graph should be walked again: never
// fetched, or fetched longer ago than maxAge.
func (s *Store) RelationsStale(ctx context.Context, animeID int, maxAge time.Duration) bool {
	var age int64
	err := s.r.QueryRowContext(ctx,
		`SELECT unixepoch() - fetched_at FROM relation_fetch WHERE anime_id = ?`, animeID).Scan(&age)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return true
	case err != nil:
		// A database hiccup is not a reason to crawl AniList.
		return false
	}
	// Inclusive, so a zero window means "always walk it again".
	return age >= int64(maxAge.Seconds())
}

// Related is everything linked to a franchise that is not one of its seasons:
// films, OVAs, specials, spin-offs and alternative versions.
func (s *Store) Related(ctx context.Context, animeID int) ([]RelatedEntry, error) {
	rows, err := s.r.QueryContext(ctx, relatedQuery, animeID, animeID, animeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []RelatedEntry{}
	for rows.Next() {
		var e RelatedEntry
		if err := rows.Scan(&e.ID, &e.Kind, &e.Romaji, &e.English, &e.Cover,
			&e.Episodes, &e.Year, &e.Format, &e.Status, &e.ListStatus,
			&e.Progress, &e.OnList); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
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
       coalesce(a.episode_count, c.episodes),
       coalesce(a.season_year, c.year, cast(substr(a.start_date, 1, 4) AS INTEGER)),
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

// EpisodeRuntime is one episode's length in minutes, 0 when unknown.
func (s *Store) EpisodeRuntime(ctx context.Context, animeID int) int {
	var minutes *int
	err := s.r.QueryRowContext(ctx,
		`SELECT duration FROM anime WHERE id = ?`, animeID).Scan(&minutes)
	if err != nil || minutes == nil {
		return 0
	}
	return *minutes
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
