package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

type Anime struct {
	ID           int
	MalID        *int
	Romaji       string
	English      *string
	Native       *string
	Synonyms     string
	Format       *string
	Status       *string
	Episodes     *int
	Duration     *int
	Season       *string
	SeasonYear   *int
	Country      *string
	IsAdult      bool
	Description  *string
	Cover        *string
	CoverColor   *string
	Banner       *string
	AverageScore *int
	MeanScore    *int
	Popularity   *int
	Favourites   *int
	Genres       string
	Studios      string
	Tags         string
	Links        string
	Source       *string
	StartDate    string
	EndDate      string
	CoverMedium  *string
	TrailerID    *string
	TrailerSite  *string
	NextEpisode  *int
	NextAiringAt *int
}

type Entry struct {
	ID          int
	AnimeID     int
	Status      *string
	Progress    int
	Score       int
	Repeat      int
	Notes       *string
	Private     bool
	Hidden      bool
	StartedAt   string
	CompletedAt string
	UpdatedAt   int
}

type ImportResult struct {
	Anime   int `json:"anime"`
	Entries int `json:"entries"`
	Removed int `json:"removed"`
}

type LibraryItem struct {
	ID int `json:"id"`

	// Both spellings travel with every item so the display preference can be
	// flipped without refetching; Title is the one it resolves to.
	Title   string  `json:"title"`
	Romaji  string  `json:"romaji"`
	English *string `json:"english"`

	Cover        *string `json:"cover"`
	Color        *string `json:"color"`
	Episodes     *int    `json:"episodes"`
	Status       *string `json:"status"`
	Progress     int     `json:"progress"`
	Score        int     `json:"score"`
	NextEpisode  *int    `json:"nextEpisode"`
	NextAiringAt *int    `json:"nextAiringAt"`

	// Where playback stopped, so the card can offer "Resume 12:04" instead of
	// making the user remember.
	Resume *Resume `json:"resume,omitempty"`
}

type Resume struct {
	EpKey      string   `json:"epKey"`
	Episode    *int     `json:"episode"`
	Position   float64  `json:"position"`
	Duration   *float64 `json:"duration"`
	Percent    float64  `json:"percent"`
	Watched    bool     `json:"watched"`
	LastPlayed int      `json:"lastPlayed"`
}

// Percent drives the progress bar; guard against a missing duration, which
// happens when a stream ends before the player reports one.
func (r *Resume) compute() {
	if r.Duration != nil && *r.Duration > 0 {
		r.Percent = r.Position / *r.Duration * 100
	}
}

const upsertAnime = `
INSERT INTO anime (
    id, mal_id, title_romaji, title_english, title_native, synonyms,
    format, status, episode_count, duration, season, season_year,
    country, is_adult, description, cover_url, cover_color, cover_medium, banner_url,
    average_score, mean_score, popularity, favourites,
    genres, studios, tags, links, source, start_date, end_date,
    trailer_id, trailer_site, next_episode, next_airing_at, synced_at
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET
    mal_id=excluded.mal_id, title_romaji=excluded.title_romaji,
    title_english=excluded.title_english, title_native=excluded.title_native,
    synonyms=excluded.synonyms, status=excluded.status,
    episode_count=excluded.episode_count, duration=excluded.duration,
    -- A stub row starts with these unset, and leaving them out of the update
    -- meant an anime hydrated later never learned it was adult, so it was
    -- searched on trackers that carry no adult titles at all.
    format=excluded.format, season=excluded.season, season_year=excluded.season_year,
    country=excluded.country, is_adult=excluded.is_adult,
    description=excluded.description, cover_url=excluded.cover_url,
    cover_color=excluded.cover_color, cover_medium=excluded.cover_medium,
    banner_url=excluded.banner_url, average_score=excluded.average_score,
    mean_score=excluded.mean_score, popularity=excluded.popularity,
    favourites=excluded.favourites, genres=excluded.genres,
    studios=excluded.studios, tags=excluded.tags, links=excluded.links,
    source=excluded.source, start_date=excluded.start_date, end_date=excluded.end_date,
    trailer_id=excluded.trailer_id, trailer_site=excluded.trailer_site,
    next_episode=excluded.next_episode, next_airing_at=excluded.next_airing_at,
    synced_at=excluded.synced_at`

const entryUpdateSet = `
    status=excluded.status, progress=excluded.progress, score=excluded.score,
    repeat_count=excluded.repeat_count, notes=excluded.notes,
    private=excluded.private, hidden=excluded.hidden,
    started_at=excluded.started_at, completed_at=excluded.completed_at,
    remote_updated_at=excluded.remote_updated_at, dirty=0`

// A row marked watched before this anime was on the AniList list is created
// locally with a synthetic negative id keyed on anime_id (see sync.go). A real
// import of that anime then arrives with AniList's own id, which a plain
// ON CONFLICT(id) misses, so the INSERT hits the anime_id UNIQUE constraint and
// aborts the whole sync. Reconciling on anime_id too lets the local row adopt
// the real id instead. The merge guard skips a row with an unpushed edit,
// which under an upsert is a no-op rather than the failing insert it replaces.
func buildUpsertEntry(guard bool) string {
	where := ""
	if guard {
		where = " WHERE list_entry.dirty = 0"
	}
	return `
INSERT INTO list_entry (
    id, anime_id, status, progress, score, repeat_count, notes,
    private, hidden, started_at, completed_at,
    remote_updated_at, local_updated_at, dirty
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,0)
ON CONFLICT(id) DO UPDATE SET` + entryUpdateSet + where + `
ON CONFLICT(anime_id) DO UPDATE SET id=excluded.id,` + entryUpdateSet + where
}

var (
	upsertEntryBase = buildUpsertEntry(false)
	upsertEntry     = buildUpsertEntry(true)
)

type ImportMode string

const (
	// Keeps local-only entries and lets an unpushed edit outrank the remote.
	ImportMerge ImportMode = "merge"
	// Overwrites unpushed edits and deletes entries missing upstream.
	ImportReplace ImportMode = "replace"
)

func (m ImportMode) valid() bool { return m == ImportMerge || m == ImportReplace }

func (s *Store) IsAdult(ctx context.Context, animeID int) (bool, error) {
	var adult int
	err := s.r.QueryRowContext(ctx,
		`SELECT coalesce(is_adult, 0) FROM anime WHERE id = ?`, animeID).Scan(&adult)
	if err != nil {
		return false, err
	}
	return adult == 1, nil
}

func (s *Store) ImportList(ctx context.Context, anime []Anime, entries []Entry, mode ImportMode) (ImportResult, error) {
	if !mode.valid() {
		mode = ImportMerge
	}

	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return ImportResult{}, err
	}
	defer tx.Rollback()

	now := time.Now().Unix()
	for _, a := range anime {
		_, err := tx.ExecContext(ctx, upsertAnime,
			a.ID, a.MalID, a.Romaji, a.English, a.Native, orJSON(a.Synonyms),
			a.Format, a.Status, a.Episodes, a.Duration, a.Season, a.SeasonYear,
			a.Country, boolInt(a.IsAdult), a.Description,
			a.Cover, a.CoverColor, a.CoverMedium, a.Banner,
			a.AverageScore, a.MeanScore, a.Popularity, a.Favourites,
			orJSON(a.Genres), orJSON(a.Studios), orJSON(a.Tags), orJSON(a.Links),
			a.Source, nullable(a.StartDate), nullable(a.EndDate),
			a.TrailerID, a.TrailerSite, a.NextEpisode, a.NextAiringAt, now)
		if err != nil {
			return ImportResult{}, err
		}
	}

	statement := upsertEntry
	if mode == ImportReplace {
		statement = upsertEntryBase
	}
	for _, e := range entries {
		_, err := tx.ExecContext(ctx, statement,
			e.ID, e.AnimeID, e.Status, e.Progress, e.Score, e.Repeat, e.Notes,
			boolInt(e.Private), boolInt(e.Hidden),
			nullable(e.StartedAt), nullable(e.CompletedAt), e.UpdatedAt, now)
		if err != nil {
			return ImportResult{}, err
		}
	}

	var removed int
	if mode == ImportReplace {
		if removed, err = dropMissingEntries(ctx, tx, entries); err != nil {
			return ImportResult{}, err
		}
	}

	if err := tx.Commit(); err != nil {
		return ImportResult{}, err
	}
	return ImportResult{Anime: len(anime), Entries: len(entries), Removed: removed}, nil
}

// Compared in Go rather than a NOT IN clause, which would hit SQLite's
// bind-parameter limit on a large list.
func dropMissingEntries(ctx context.Context, tx *sql.Tx, keep []Entry) (int, error) {
	wanted := make(map[int]struct{}, len(keep))
	for _, e := range keep {
		wanted[e.AnimeID] = struct{}{}
	}

	rows, err := tx.QueryContext(ctx, `SELECT anime_id FROM list_entry`)
	if err != nil {
		return 0, err
	}

	var stale []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		if _, ok := wanted[id]; !ok {
			stale = append(stale, id)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	for _, id := range stale {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM list_entry WHERE anime_id = ?`, id); err != nil {
			return 0, err
		}
	}
	return len(stale), nil
}

// The window function picks the most recently played episode per anime, which
// is what "continue watching" means when several were started.
const latestPlayback = `
LEFT JOIN (
    SELECT anime_id, ep_key, position_s, duration_s, watched, last_played_at,
           ROW_NUMBER() OVER (PARTITION BY anime_id ORDER BY last_played_at DESC) AS rn
    FROM playback
) p ON p.anime_id = a.id AND p.rn = 1
LEFT JOIN episode ep ON ep.anime_id = a.id AND ep.ep_key = p.ep_key`

const libraryColumns = `
a.id, a.title_romaji, a.title_english, a.cover_url, a.cover_color, a.episode_count,
e.status, e.progress, e.score, a.next_episode, a.next_airing_at,
p.ep_key, ep.number, p.position_s, p.duration_s, p.watched, p.last_played_at`

// LibrarySorts are the orderings the library accepts; the first is the default.
var LibrarySorts = []string{"updated", "title", "score", "airing", "progress"}

var librarySortSQL = map[string]string{
	"updated":  "e.local_updated_at DESC, a.title_romaji",
	"title":    "lower(coalesce(a.title_english, a.title_romaji)), a.title_romaji",
	"score":    "e.score DESC, e.local_updated_at DESC",
	"airing":   "a.next_airing_at IS NULL, a.next_airing_at, a.title_romaji",
	"progress": "e.progress DESC, e.local_updated_at DESC",
}

const libraryWhere = `
WHERE (?1 = '' OR e.status = ?1)
  AND (?2 = '' OR a.title_romaji LIKE ?2 OR a.title_english LIKE ?2 OR a.synonyms LIKE ?2)`

func libraryQuery(sort string) string {
	order, ok := librarySortSQL[sort]
	if !ok {
		order = librarySortSQL[LibrarySorts[0]]
	}
	return `
SELECT ` + libraryColumns + `
FROM list_entry e
JOIN anime a ON a.id = e.anime_id
` + latestPlayback + libraryWhere + `
ORDER BY ` + order + `
LIMIT ?3 OFFSET ?4`
}

// Continue watching: episodes started but not finished, most recent first. The
// filter lives inside the window so the row shows the episode that qualified the
// show. "Not finished" is position vs threshold (same rule as ResumeAt); the 15s
// floor matches resumable() so the rail and the resume prompt agree.
const inProgress = `dismissed = 0 AND position_s >= 15
      AND NOT (coalesce(duration_s, 0) > 0 AND position_s >= duration_s * ?)
      AND NOT (coalesce(duration_s, 0) <= 0 AND watched = 1)`

const continueQuery = `
SELECT ` + libraryColumns + `
FROM anime a
JOIN (
    SELECT anime_id, ep_key, position_s, duration_s, watched, last_played_at,
           ROW_NUMBER() OVER (PARTITION BY anime_id ORDER BY last_played_at DESC) AS rn
    FROM playback
    WHERE ` + inProgress + `
) p ON p.anime_id = a.id AND p.rn = 1
LEFT JOIN list_entry e ON e.anime_id = a.id
LEFT JOIN episode ep ON ep.anime_id = a.id AND ep.ep_key = p.ep_key
ORDER BY p.last_played_at DESC
LIMIT ? OFFSET ?`

// One extra row is requested so the envelope can report hasMore without a
// second query.
// LibraryFilter narrows and orders the list. An empty Sort is the default
// ordering; an unknown one falls back to it rather than failing.
type LibraryFilter struct {
	Status string
	Query  string
	Sort   string
}

func (s *Store) Library(ctx context.Context, f LibraryFilter, p Paging) (Page[LibraryItem], error) {
	like := ""
	if q := strings.TrimSpace(f.Query); q != "" {
		like = "%" + q + "%"
	}
	items, err := s.libraryRows(ctx, libraryQuery(f.Sort), f.Status, like, p.PerPage+1, p.Offset())
	if err != nil {
		return Page[LibraryItem]{}, err
	}

	total, err := s.countRows(ctx,
		`SELECT count(*) FROM list_entry e JOIN anime a ON a.id = e.anime_id`+libraryWhere, f.Status, like)
	if err != nil {
		return Page[LibraryItem]{}, err
	}
	return NewPage(items, p, total), nil
}

type LibraryCounts struct {
	Total      int            `json:"total"`
	Statuses   map[string]int `json:"statuses"`
	Favourites int            `json:"favourites"`
}

// LibraryCounts is how many entries carry each list status, for the tabs.
func (s *Store) LibraryCounts(ctx context.Context) (LibraryCounts, error) {
	out := LibraryCounts{Statuses: map[string]int{}}
	rows, err := s.r.QueryContext(ctx, `
		SELECT e.status, count(*) FROM list_entry e
		JOIN anime a ON a.id = e.anime_id
		GROUP BY e.status`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var status *string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return out, err
		}
		if status != nil {
			out.Statuses[*status] = n
		}
		out.Total += n
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	out.Favourites, err = s.countRows(ctx,
		`SELECT count(*) FROM user_anime WHERE favourite = 1 AND hidden = 0`)
	return out, err
}

func (s *Store) ContinueWatching(ctx context.Context, p Paging) (Page[LibraryItem], error) {
	threshold := s.watchedThreshold(ctx)
	items, err := s.libraryRows(ctx, continueQuery, threshold, p.PerPage+1, p.Offset())
	if err != nil {
		return Page[LibraryItem]{}, err
	}

	total, err := s.countRows(ctx,
		`SELECT count(DISTINCT anime_id) FROM playback WHERE `+inProgress, threshold)
	if err != nil {
		return Page[LibraryItem]{}, err
	}
	return NewPage(items, p, total), nil
}

func (s *Store) countRows(ctx context.Context, query string, args ...any) (int, error) {
	var n int
	err := s.r.QueryRowContext(ctx, query, args...).Scan(&n)
	return n, err
}

func (s *Store) libraryRows(ctx context.Context, query string, args ...any) ([]LibraryItem, error) {
	mode := s.TitleMode(ctx, 0)

	rows, err := s.r.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []LibraryItem{}
	for rows.Next() {
		var (
			it      LibraryItem
			status  *string
			prog    *int
			score   *int
			epKey   *string
			epNum   *int
			pos     *float64
			dur     *float64
			watched *int
			played  *int
		)
		if err := rows.Scan(&it.ID, &it.Romaji, &it.English, &it.Cover, &it.Color, &it.Episodes,
			&status, &prog, &score, &it.NextEpisode, &it.NextAiringAt,
			&epKey, &epNum, &pos, &dur, &watched, &played); err != nil {
			return nil, err
		}

		it.Title = PickTitle(mode, it.Romaji, it.English)
		it.Status = status
		if prog != nil {
			it.Progress = *prog
		}
		if score != nil {
			it.Score = *score
		}
		if epKey != nil && pos != nil {
			r := &Resume{
				EpKey: *epKey, Episode: epNum, Position: *pos, Duration: dur,
				Watched: watched != nil && *watched == 1,
			}
			if played != nil {
				r.LastPlayed = *played
			}
			r.compute()
			it.Resume = r
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func clampLimit(v, def, max int) int {
	if v <= 0 || v > max {
		return def
	}
	return v
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// The JSON columns are NOT NULL, so an unset field has to become an empty
// array rather than the zero string.
func orJSON(s string) string {
	if s == "" {
		return "[]"
	}
	return s
}
