package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"kuro/internal/corpus"
	"kuro/internal/match"
	"kuro/internal/metadata"
)

type CorpusEntry struct {
	AniListID int
	MalID     int
	AniDBID   int
	Kind      string
	Episodes  int
	Year      int
	Season    string
	Titles    []CorpusTitle
}

type CorpusTitle struct {
	Text string
	Kind string
	Lang string
}

type CorpusStats struct {
	Anime  int `json:"anime"`
	Titles int `json:"titles"`
	Dead   int `json:"dead"`
}

const upsertCorpusAnime = `
INSERT INTO corpus_anime (anime_id, mal_id, anidb_id, kind, episodes, year, season, titles_at)
VALUES (?,?,?,?,?,?,?,?)
ON CONFLICT(anime_id) DO UPDATE SET
    mal_id=coalesce(excluded.mal_id, corpus_anime.mal_id),
    anidb_id=coalesce(excluded.anidb_id, corpus_anime.anidb_id),
    kind=coalesce(excluded.kind, corpus_anime.kind),
    episodes=coalesce(excluded.episodes, corpus_anime.episodes),
    year=coalesce(excluded.year, corpus_anime.year),
    season=coalesce(excluded.season, corpus_anime.season),
    titles_at=excluded.titles_at`

const insertTitle = `
INSERT INTO title (anime_id, text, norm, kind, lang) VALUES (?,?,?,?,?)
ON CONFLICT(anime_id, text) DO NOTHING`

// SaveCorpus writes entries in one transaction. Two hundred thousand title
// rows one statement at a time is minutes; batched it is seconds.
func (s *Store) SaveCorpus(ctx context.Context, entries []CorpusEntry) (int, error) {
	if len(entries) == 0 {
		return 0, nil
	}

	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	animeStmt, err := tx.PrepareContext(ctx, upsertCorpusAnime)
	if err != nil {
		return 0, err
	}
	defer animeStmt.Close()

	titleStmt, err := tx.PrepareContext(ctx, insertTitle)
	if err != nil {
		return 0, err
	}
	defer titleStmt.Close()

	now := time.Now().Unix()
	var written int

	for _, e := range entries {
		if e.AniListID == 0 {
			continue
		}
		if _, err := animeStmt.ExecContext(ctx, e.AniListID,
			zeroToNull(e.MalID), zeroToNull(e.AniDBID), nullable(e.Kind),
			zeroToNull(e.Episodes), zeroToNull(e.Year), nullable(e.Season), now); err != nil {
			return 0, err
		}

		for _, t := range e.Titles {
			if t.Text == "" {
				continue
			}
			norm := corpus.Normalise(t.Text)
			if norm == "" {
				continue
			}
			if _, err := titleStmt.ExecContext(ctx, e.AniListID, t.Text, norm,
				t.Kind, nullable(t.Lang)); err != nil {
				return 0, err
			}
			written++
		}
	}

	return written, tx.Commit()
}

func (s *Store) MarkSource(ctx context.Context, name string, records int) error {
	_, err := s.w.ExecContext(ctx,
		`INSERT INTO corpus_source (name, refreshed_at, records) VALUES (?,?,?)
		 ON CONFLICT(name) DO UPDATE SET refreshed_at=excluded.refreshed_at, records=excluded.records`,
		name, time.Now().Unix(), records)
	return err
}

func (s *Store) SourceRefreshedAt(ctx context.Context, name string) (time.Time, error) {
	var at int64
	err := s.r.QueryRowContext(ctx,
		`SELECT refreshed_at FROM corpus_source WHERE name = ?`, name).Scan(&at)
	if err == sql.ErrNoRows {
		return time.Time{}, nil
	}
	return time.Unix(at, 0), err
}

// RandomIDs draws uniformly from every anime in the corpus, not just the ones
// anybody has heard of. More than one is returned so the caller can skip ids
// AniList no longer serves without a second round trip.
func (s *Store) RandomIDs(ctx context.Context, kind string, year, n int) ([]int, error) {
	if n <= 0 || n > 25 {
		n = 5
	}

	rows, err := s.r.QueryContext(ctx, `
		SELECT anime_id FROM corpus_anime
		WHERE (?1 = '' OR upper(kind) = upper(?1))
		  AND (?2 = 0 OR year = ?2)
		ORDER BY random()
		LIMIT ?3`, kind, year, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// CorpusRecord is the minimal description held for every anime, whichever
// catalogue it came from. It is what remains when the upstream metadata API
// cannot be reached.
type CorpusRecord struct {
	AnimeID  int      `json:"id"`
	MalID    int      `json:"malId,omitempty"`
	Kind     string   `json:"format,omitempty"`
	Episodes int      `json:"episodes,omitempty"`
	Year     int      `json:"year,omitempty"`
	Season   string   `json:"season,omitempty"`
	Primary  string   `json:"romaji"`
	Titles   []string `json:"titles,omitempty"`
}

func (s *Store) CorpusRecord(ctx context.Context, animeID int) (CorpusRecord, error) {
	var rec CorpusRecord
	var malID, episodes, year *int
	var kind, season *string

	err := s.r.QueryRowContext(ctx, `
		SELECT anime_id, mal_id, kind, episodes, year, season
		FROM corpus_anime WHERE anime_id = ?`, animeID).
		Scan(&rec.AnimeID, &malID, &kind, &episodes, &year, &season)
	if errors.Is(err, sql.ErrNoRows) {
		return CorpusRecord{}, nil
	}
	if err != nil {
		return CorpusRecord{}, err
	}

	rec.MalID = deref(malID)
	rec.Episodes = deref(episodes)
	rec.Year = deref(year)
	rec.Kind = derefString(kind)
	rec.Season = derefString(season)

	rows, err := s.r.QueryContext(ctx,
		`SELECT text, kind FROM title WHERE anime_id = ? ORDER BY kind = 'primary' DESC`, animeID)
	if err != nil {
		return rec, err
	}
	defer rows.Close()

	for rows.Next() {
		var text, kind string
		if err := rows.Scan(&text, &kind); err != nil {
			return rec, err
		}
		if kind == "primary" && rec.Primary == "" {
			rec.Primary = text
		}
		rec.Titles = append(rec.Titles, text)
	}
	if rec.Primary == "" && len(rec.Titles) > 0 {
		rec.Primary = rec.Titles[0]
	}
	return rec, rows.Err()
}

func derefString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func (s *Store) CorpusStats(ctx context.Context) (CorpusStats, error) {
	var out CorpusStats
	err := s.r.QueryRowContext(ctx, `
		SELECT (SELECT count(*) FROM corpus_anime),
		       (SELECT count(*) FROM title),
		       (SELECT count(*) FROM dead_anime)`).Scan(&out.Anime, &out.Titles, &out.Dead)
	return out, err
}

// KnownIDs is used to diff against a freshly downloaded id list, so only
// genuinely new anime cost an AniList request.
func (s *Store) KnownIDs(ctx context.Context) (map[int]struct{}, error) {
	rows, err := s.r.QueryContext(ctx,
		`SELECT anime_id FROM corpus_anime UNION SELECT anime_id FROM dead_anime`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[int]struct{}, 24000)
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = struct{}{}
	}
	return out, rows.Err()
}

func (s *Store) MarkDead(ctx context.Context, ids []int) error {
	if len(ids) == 0 {
		return nil
	}
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, id := range ids {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO dead_anime (anime_id) VALUES (?) ON CONFLICT DO NOTHING`, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// BuildIndex loads the whole corpus into the in-memory matcher. The primary
// title is placed first because the matcher rewards it.
func (s *Store) BuildIndex(ctx context.Context) (*match.Index, error) {
	// episodes and year are nullable; coalescing here keeps a single unknown
	// year from failing the entire index build.
	rows, err := s.r.QueryContext(ctx, `
		SELECT c.anime_id, coalesce(c.kind, ''), coalesce(c.episodes, 0), coalesce(c.year, 0),
		       t.text, t.kind
		FROM corpus_anime c
		JOIN title t ON t.anime_id = c.anime_id
		ORDER BY c.anime_id, (t.kind != 'primary'), t.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	ix := match.NewIndex()
	var (
		cur     match.Media
		haveCur bool
	)

	for rows.Next() {
		var (
			id, episodes, year         int
			kind, titleText, titleKind string
		)
		if err := rows.Scan(&id, &kind, &episodes, &year, &titleText, &titleKind); err != nil {
			return nil, err
		}

		if haveCur && cur.ID != id {
			ix.Add(cur)
			cur = match.Media{}
		}
		if cur.ID == 0 {
			cur = match.Media{ID: id, Format: kind, Episodes: episodes, Year: year}
		}
		cur.Titles = append(cur.Titles, titleText)
		haveCur = true
	}
	if haveCur && cur.ID != 0 {
		ix.Add(cur)
	}
	return ix, rows.Err()
}

// MatchBoosts weights anime the user has a relationship with, so a release of
// a franchise they are midway through resolves to the season they are on.
func (s *Store) MatchBoosts(ctx context.Context) (map[int]float64, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT anime_id, coalesce(status, '') FROM list_entry`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[int]float64, 512)
	for rows.Next() {
		var id int
		var status string
		if err := rows.Scan(&id, &status); err != nil {
			return nil, err
		}
		switch status {
		case "CURRENT", "REPEATING":
			out[id] = match.BoostWatching
		case "PLANNING", "PAUSED":
			out[id] = match.BoostPlanned
		default:
			out[id] = match.BoostCompleted
		}
	}
	return out, rows.Err()
}

// SeaDexInfo is the curation signal for one release, looked up by infohash or
// by release group when the tracker redacts the hash.
type SeaDexInfo struct {
	IsBest    bool
	DualAudio bool
	Group     string
	Notes     string
}

func (s *Store) SaveSeaDex(ctx context.Context, releases []metadata.SeaDexRelease) (int, error) {
	if len(releases) == 0 {
		return 0, nil
	}

	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	// A full replace: an entry removed upstream should stop being trusted.
	if _, err := tx.ExecContext(ctx, `DELETE FROM seadex`); err != nil {
		return 0, err
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO seadex (anime_id, info_hash, release_group, tracker, is_best, dual_audio, tags, url)
		VALUES (?,?,?,?,?,?,?,?)
		ON CONFLICT(anime_id, info_hash, release_group) DO UPDATE SET
		    is_best=excluded.is_best, dual_audio=excluded.dual_audio, tags=excluded.tags`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	var written int
	for _, r := range releases {
		tags, _ := json.Marshal(r.Tags)
		if _, err := stmt.ExecContext(ctx, r.AniListID, r.InfoHash, nullable(r.ReleaseGroup),
			nullable(r.Tracker), boolInt(r.IsBest), boolInt(r.DualAudio),
			string(tags), nullable(r.URL)); err != nil {
			return 0, err
		}
		written++
	}
	return written, tx.Commit()
}

// BestGroups returns the release groups SeaDex considers best for an anime.
// Searching for the group directly is far more effective than hoping the
// curated release turns up in a generic title search.
func (s *Store) BestGroups(ctx context.Context, animeID int) ([]string, error) {
	rows, err := s.r.QueryContext(ctx, `
		SELECT DISTINCT release_group FROM seadex
		WHERE anime_id = ? AND is_best = 1 AND release_group IS NOT NULL AND release_group <> ''`,
		animeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var g string
		if err := rows.Scan(&g); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// SeaDexFor returns the curation for one anime, indexed both ways: by hash for
// an exact match, and lowercased by group for entries whose hash is redacted.
func (s *Store) SeaDexFor(ctx context.Context, animeID int) (byHash, byGroup map[string]SeaDexInfo, err error) {
	rows, err := s.r.QueryContext(ctx, `
		SELECT info_hash, coalesce(release_group, ''), is_best, dual_audio
		FROM seadex WHERE anime_id = ?`, animeID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	byHash = map[string]SeaDexInfo{}
	byGroup = map[string]SeaDexInfo{}

	for rows.Next() {
		var hash, group string
		var best, dual int
		if err := rows.Scan(&hash, &group, &best, &dual); err != nil {
			return nil, nil, err
		}
		info := SeaDexInfo{IsBest: best == 1, DualAudio: dual == 1, Group: group}
		if hash != "" {
			byHash[strings.ToLower(hash)] = info
		}
		if group != "" {
			// A group listed as best anywhere counts as endorsed.
			existing, seen := byGroup[strings.ToLower(group)]
			if !seen || (!existing.IsBest && info.IsBest) {
				byGroup[strings.ToLower(group)] = info
			}
		}
	}
	return byHash, byGroup, rows.Err()
}

func (s *Store) SeaDexCount(ctx context.Context) (int, error) {
	var n int
	err := s.r.QueryRowContext(ctx, `SELECT count(*) FROM seadex`).Scan(&n)
	return n, err
}

func zeroToNull(v int) any {
	if v == 0 {
		return nil
	}
	return v
}
