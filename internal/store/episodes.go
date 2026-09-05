package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"kuro/internal/metadata"
)

type EpisodeRow struct {
	AnimeID  int     `json:"animeId"`
	EpKey    string  `json:"epKey"`
	Number   int     `json:"number"`
	Absolute *int    `json:"absolute,omitempty"`
	TitleEN  *string `json:"titleEn,omitempty"`
	TitleJA  *string `json:"titleJa,omitempty"`
	Overview *string `json:"overview,omitempty"`
	Still    *string `json:"still,omitempty"`
	AirDate  *int64  `json:"airDate,omitempty"`
	Runtime  *int    `json:"runtime,omitempty"`

	// Display equals Number now that episodes carry the entry's own numbering;
	// kept because the pages read it.
	Display int `json:"display"`

	// Planned marks a row invented from the catalogue's episode count. Worth
	// offering to play, but not evidence the episode exists.
	Planned bool `json:"planned,omitempty"`

	Filler   string  `json:"filler,omitempty"`
	Recap    bool    `json:"recap"`
	Watched  bool    `json:"watched"`
	Position float64 `json:"position,omitempty"`
	Duration float64 `json:"duration,omitempty"`
	// Resumable says the position is worth going back to, by the same rule
	// playback uses, so the list and the player agree.
	Resumable bool `json:"resumable,omitempty"`
	OnDisk    bool `json:"onDisk"`
}

func (s *Store) SaveEpisodes(ctx context.Context, animeID int, eps []metadata.Episode) (int, error) {
	if len(eps) == 0 {
		return 0, nil
	}

	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO episode (anime_id, ep_key, number, tvdb_number, absolute, season_number, is_special,
		                     title_en, title_ja, overview, still_url, air_date, runtime, anidb_eid)
		VALUES (?,?,?,?,?,?,0,?,?,?,?,?,?,?)
		ON CONFLICT(anime_id, ep_key) DO UPDATE SET
		    tvdb_number=coalesce(excluded.tvdb_number, episode.tvdb_number),
		    absolute=coalesce(excluded.absolute, episode.absolute),
		    title_en=coalesce(excluded.title_en, episode.title_en),
		    title_ja=coalesce(excluded.title_ja, episode.title_ja),
		    overview=coalesce(excluded.overview, episode.overview),
		    still_url=coalesce(excluded.still_url, episode.still_url),
		    air_date=coalesce(excluded.air_date, episode.air_date),
		    runtime=coalesce(excluded.runtime, episode.runtime)`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	keys := make([]any, 0, len(eps)+1)
	keys = append(keys, animeID)
	for _, e := range eps {
		if _, err := stmt.ExecContext(ctx, animeID, fmt.Sprint(e.Number), e.Number,
			nullableInt(e.TvdbNumber), nullableInt(e.Absolute), nullableInt(e.Season),
			nullable(e.TitleEN), nullable(e.TitleJA), nullable(e.Overview),
			nullable(e.Image), nullableInt64(e.AirDate), nullableInt(e.Runtime),
			nullableInt(e.AniDBID)); err != nil {
			return 0, err
		}
		keys = append(keys, fmt.Sprint(e.Number))
	}

	// The source's list is the whole list: a numbered row it no longer has was
	// keyed under an older numbering.
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM episode WHERE anime_id = ? AND is_special = 0
		  AND ep_key NOT IN (`+placeholders(len(eps))+`)`, keys...); err != nil {
		return 0, err
	}
	return len(eps), tx.Commit()
}

func (s *Store) SaveFillers(ctx context.Context, rows []metadata.Filler) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}

	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	// user_kind is left alone: a manual correction outlives a dataset refresh.
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO filler (mal_id, number, kind, source) VALUES (?,?,?,'anifiller')
		ON CONFLICT(mal_id, number) DO UPDATE SET kind=excluded.kind, source=excluded.source`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	for _, f := range rows {
		if _, err := stmt.ExecContext(ctx, f.MalID, f.Episode, f.Kind); err != nil {
			return 0, err
		}
	}
	return len(rows), tx.Commit()
}

// UnclassifiedEpisodes counts the episodes of a show that no source has
// classified. Zero means there is nothing a second source could add, which is
// the usual answer and worth knowing before fetching anything.
func (s *Store) UnclassifiedEpisodes(ctx context.Context, animeID int) (int, error) {
	var n int
	err := s.r.QueryRowContext(ctx, `
		SELECT count(*)
		FROM episode e
		JOIN anime a ON a.id = e.anime_id
		LEFT JOIN filler f ON f.mal_id = a.mal_id AND f.number = e.number
		WHERE e.anime_id = ? AND a.mal_id IS NOT NULL
		  AND (f.kind IS NULL OR f.kind IN ('', 'unknown'))`, animeID).Scan(&n)
	return n, err
}

// SaveFillerGaps fills in episodes the dataset has no classification for,
// leaving everything it does classify alone: it keeps the last word wherever it
// already has a classification.
func (s *Store) SaveFillerGaps(ctx context.Context, malID int, rows []metadata.Filler) (int, error) {
	if malID == 0 || len(rows) == 0 {
		return 0, nil
	}

	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO filler (mal_id, number, kind, source)
		VALUES (?,?,?,'animefillerlist')
		ON CONFLICT(mal_id, number) DO UPDATE SET
		    kind = CASE WHEN filler.kind IN ('', 'unknown') THEN excluded.kind ELSE filler.kind END,
		    source = CASE WHEN filler.kind IN ('', 'unknown') THEN excluded.source ELSE filler.source END`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	var filled int
	for _, r := range rows {
		if r.Episode == 0 || r.Kind == "" || r.Kind == "unknown" {
			continue
		}
		res, err := stmt.ExecContext(ctx, malID, r.Episode, r.Kind)
		if err != nil {
			return 0, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			filled++
		}
	}
	return filled, tx.Commit()
}

// SaveFlags records recap markers without disturbing the classification the
// filler dataset already provided: an episode can be both.
func (s *Store) SaveFlags(ctx context.Context, malID int, flags []metadata.EpisodeFlags) (int, error) {
	if malID == 0 || len(flags) == 0 {
		return 0, nil
	}

	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var recaps int
	for _, f := range flags {
		kind := "unknown"
		if f.Filler {
			kind = "filler"
		}
		if f.Recap {
			recaps++
		}

		if _, err := tx.ExecContext(ctx, `
			INSERT INTO filler (mal_id, number, kind, source, recap, recap_source)
			VALUES (?,?,?,'jikan',?, 'jikan')
			ON CONFLICT(mal_id, number) DO UPDATE SET
			    recap=excluded.recap,
			    recap_source=excluded.recap_source,
			    kind=CASE WHEN filler.kind IN ('', 'unknown') THEN excluded.kind ELSE filler.kind END`,
			malID, f.Episode, kind, boolInt(f.Recap)); err != nil {
			return 0, err
		}
	}
	return recaps, tx.Commit()
}

func (s *Store) SaveSkips(ctx context.Context, malID, episode int, skips []metadata.SkipTime) error {
	if len(skips) == 0 {
		return nil
	}

	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, k := range skips {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO skip_time (mal_id, number, kind, start_s, end_s, skip_id)
			VALUES (?,?,?,?,?,?)
			ON CONFLICT(mal_id, number, kind) DO UPDATE SET
			    start_s=excluded.start_s, end_s=excluded.end_s, skip_id=excluded.skip_id`,
			malID, episode, k.Kind, k.Start, k.End, nullable(k.SkipID)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Episodes joins metadata, filler classification, watch state and what is
// cached. Watched is the player's tick or the list's progress, whichever says so.
const episodeListQuery = `
SELECT e.anime_id, e.ep_key, e.number, e.absolute,
       e.title_en, e.title_ja, e.overview, e.still_url, e.air_date, e.runtime,
       coalesce(f.user_kind, f.kind, ''), coalesce(f.recap, 0),
       coalesce(p.watched, 0) OR e.number <= coalesce(l.progress, 0),
       coalesce(p.position_s, 0), coalesce(p.duration_s, 0),
       EXISTS (SELECT 1 FROM torrent_file tf
               WHERE tf.anime_id = e.anime_id AND tf.ep_key = e.ep_key AND tf.selected = 1)
FROM episode e
LEFT JOIN anime a      ON a.id = e.anime_id
LEFT JOIN list_entry l ON l.anime_id = e.anime_id
LEFT JOIN filler f     ON f.mal_id = a.mal_id AND f.number = e.number
LEFT JOIN playback p   ON p.anime_id = e.anime_id AND p.ep_key = e.ep_key
WHERE e.anime_id = ?
ORDER BY e.number`

func (s *Store) Episodes(ctx context.Context, animeID int) ([]EpisodeRow, error) {
	// Resolved before the query so its reads don't need a second read-pool
	// connection while this cursor holds one — under load that deadlocks the pool.
	threshold := s.thresholdFor(ctx, animeID)

	rows, err := s.r.QueryContext(ctx, episodeListQuery, animeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []EpisodeRow{}
	for rows.Next() {
		var e EpisodeRow
		var watched, recap int
		if err := rows.Scan(&e.AnimeID, &e.EpKey, &e.Number, &e.Absolute,
			&e.TitleEN, &e.TitleJA, &e.Overview, &e.Still, &e.AirDate, &e.Runtime,
			&e.Filler, &recap, &watched, &e.Position, &e.Duration, &e.OnDisk); err != nil {
			return nil, err
		}
		e.Watched = watched == 1
		e.Resumable = resumable(e.Position, e.Duration, e.Watched, threshold)
		e.Recap = recap == 1
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if len(out) == 0 {
		return s.plannedEpisodes(ctx, animeID)
	}

	out = s.padToCount(ctx, animeID, out)
	for i := range out {
		out[i].Display = out[i].Number
	}
	return out, nil
}

// padToCount fills in the episodes the catalogue says exist but nothing has
// recorded. Only for release-derived lists, which stop at what's published; a
// real metadata source lists unaired episodes itself.
func (s *Store) padToCount(ctx context.Context, animeID int, out []EpisodeRow) []EpisodeRow {
	for _, e := range out {
		if e.TitleEN != nil || e.AirDate != nil {
			return out
		}
	}

	var total int
	if err := s.r.QueryRowContext(ctx,
		`SELECT coalesce(episode_count, 0) FROM anime WHERE id = ?`, animeID).Scan(&total); err != nil {
		return out
	}

	last := out[len(out)-1].Number
	first := out[0].Number
	// Counted from where the derived list starts, not from one: a later cour
	// numbered 13-18 still needs `total` episodes, ending at 24.
	for n := last + 1; n-first+1 <= total && n-last <= 5000; n++ {
		out = append(out, EpisodeRow{
			AnimeID: animeID,
			EpKey:   strconv.Itoa(n),
			Number:  n,
			Planned: true,
		})
	}
	return out
}

// plannedEpisodes stands in when a show has an episode count but no per-episode
// metadata; without it the show reads as having no episodes at all.
func (s *Store) plannedEpisodes(ctx context.Context, animeID int) ([]EpisodeRow, error) {
	// The corpus knows the count for shows never imported, MAL-only ones included.
	total, _ := s.EpisodeCount(ctx, animeID)
	if total <= 0 {
		return []EpisodeRow{}, nil
	}
	if total > 5000 {
		total = 5000
	}

	played, err := s.playbackByEpisode(ctx, animeID)
	if err != nil {
		return nil, err
	}
	cached, err := s.cachedEpisodes(ctx, animeID)
	if err != nil {
		return nil, err
	}
	entry, err := s.ListEntry(ctx, animeID)
	if err != nil {
		return nil, err
	}

	threshold := s.thresholdFor(ctx, animeID)
	out := make([]EpisodeRow, 0, total)
	for n := 1; n <= total; n++ {
		key := strconv.Itoa(n)
		row := EpisodeRow{AnimeID: animeID, EpKey: key, Number: n, Display: n, Planned: true}
		if p, ok := played[key]; ok {
			row.Watched, row.Position, row.Duration = p.watched, p.position, p.duration
			row.Resumable = resumable(p.position, p.duration, p.watched, threshold)
		}
		row.Watched = row.Watched || n <= entry.Progress
		_, row.OnDisk = cached[key]
		out = append(out, row)
	}
	return out, nil
}

type playedEpisode struct {
	watched  bool
	position float64
	duration float64
}

func (s *Store) playbackByEpisode(ctx context.Context, animeID int) (map[string]playedEpisode, error) {
	rows, err := s.r.QueryContext(ctx,
		`SELECT ep_key, watched, position_s, coalesce(duration_s, 0) FROM playback WHERE anime_id = ?`,
		animeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]playedEpisode{}
	for rows.Next() {
		var key string
		var watched int
		var p playedEpisode
		if err := rows.Scan(&key, &watched, &p.position, &p.duration); err != nil {
			return nil, err
		}
		p.watched = watched == 1
		out[key] = p
	}
	return out, rows.Err()
}

func (s *Store) cachedEpisodes(ctx context.Context, animeID int) (map[string]struct{}, error) {
	rows, err := s.r.QueryContext(ctx,
		`SELECT ep_key FROM torrent_file WHERE anime_id = ? AND selected = 1`, animeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]struct{}{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		out[key] = struct{}{}
	}
	return out, rows.Err()
}

// NextEpisode is the episode to play after `after`: the next number, or with
// playback.skip_filler on for the show, the next that is neither filler nor a
// recap. Zero when the show ends there. Without episode data the count alone
// decides, since nothing is known to skip.
func (s *Store) NextEpisode(ctx context.Context, animeID, after int) (int, error) {
	prefs, err := s.Prefs(ctx, animeID)
	if err != nil {
		return 0, err
	}
	total, _ := s.EpisodeCount(ctx, animeID)

	if !prefs.Bool("playback.skip_filler") {
		if total > 0 && after+1 > total {
			return 0, nil
		}
		return s.onceAired(ctx, animeID, after+1)
	}

	var next *int
	err = s.r.QueryRowContext(ctx, `
		SELECT min(e.number) FROM episode e
		LEFT JOIN anime a  ON a.id = e.anime_id
		LEFT JOIN filler f ON f.mal_id = a.mal_id AND f.number = e.number
		WHERE e.anime_id = ? AND e.is_special = 0 AND e.number > ?
		  AND coalesce(f.user_kind, f.kind, '') <> 'filler' AND coalesce(f.recap, 0) = 0`,
		animeID, after).Scan(&next)
	if err != nil {
		return 0, err
	}
	if next == nil {
		// Nothing catalogued past here. Rows can lag the count on an airing
		// show, so the first uncatalogued number is still offered.
		var last int
		if err := s.r.QueryRowContext(ctx,
			`SELECT coalesce(max(number), 0) FROM episode WHERE anime_id = ? AND is_special = 0`, animeID).Scan(&last); err != nil {
			return 0, err
		}
		n := max(last, after) + 1
		next = &n
	}
	if total > 0 && *next > total {
		return 0, nil
	}
	return s.onceAired(ctx, animeID, *next)
}

// onceAired keeps auto-next and prefetch off an episode the catalogue says is
// still to broadcast: there is nothing to find, only wrong things.
func (s *Store) onceAired(ctx context.Context, animeID, episode int) (int, error) {
	var nextEpisode int
	var airingAt int64
	err := s.r.QueryRowContext(ctx,
		`SELECT coalesce(next_episode, 0), coalesce(next_airing_at, 0) FROM anime WHERE id = ?`,
		animeID).Scan(&nextEpisode, &airingAt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	if !aired(episode, nextEpisode, airingAt, time.Now().Unix()) {
		return 0, nil
	}
	return episode, nil
}

// EpisodeAlias is the other numbers a release may carry for one episode: TheTVDB's
// count through a long season (Bleach TYBW part 4's 5 is 45 there) and the
// absolute count over the whole show. Zero where unknown or the same number.
type EpisodeAlias struct {
	Tvdb     int
	Absolute int
	// Stated: the catalogue carried a numbering; a derived one must not override it.
	Stated bool
}

// Numbers lists the aliases that differ from the episode's own number.
func (a EpisodeAlias) Numbers() []int {
	var out []int
	for _, n := range []int{a.Tvdb, a.Absolute} {
		if n > 0 && !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out
}

func (s *Store) EpisodeAlias(ctx context.Context, animeID, episode int) (EpisodeAlias, error) {
	var tvdb, absolute *int
	err := s.r.QueryRowContext(ctx,
		`SELECT tvdb_number, absolute FROM episode WHERE anime_id = ? AND ep_key = ?`,
		animeID, strconv.Itoa(episode)).Scan(&tvdb, &absolute)
	if err != nil {
		// No catalogue row is the normal case for an unlisted show.
		return EpisodeAlias{}, nil
	}

	out := EpisodeAlias{Stated: tvdb != nil || absolute != nil}
	if tvdb != nil && *tvdb != episode {
		out.Tvdb = *tvdb
	}
	if absolute != nil && *absolute != episode {
		out.Absolute = *absolute
	}
	return out, nil
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func (s *Store) MalID(ctx context.Context, animeID int) (int, error) {
	var mal *int
	err := s.r.QueryRowContext(ctx, `
		SELECT coalesce(a.mal_id, c.mal_id)
		FROM corpus_anime c
		LEFT JOIN anime a ON a.id = c.anime_id
		WHERE c.anime_id = ?
		UNION ALL SELECT mal_id FROM anime WHERE id = ?
		LIMIT 1`, animeID, animeID).Scan(&mal)
	if err != nil || mal == nil {
		return 0, nil
	}
	return *mal, nil
}

// Stills and titles appear days after broadcast, so an incomplete list is
// refetched on a timer; a finished and complete one never is.
const episodeCoverageQuery = `
SELECT (SELECT count(*) FROM episode WHERE anime_id = ?),
       (SELECT count(*) FROM episode WHERE anime_id = ?
          AND (still_url IS NULL OR still_url = '' OR title_en IS NULL OR title_en = '')),
       coalesce((SELECT status FROM anime WHERE id = ?), ''),
       coalesce((SELECT episode_count FROM anime WHERE id = ?), 0)`

func (s *Store) EpisodesStale(ctx context.Context, animeID int, maxAge time.Duration) bool {
	var stored, incomplete, expected int
	var status string
	if err := s.r.QueryRowContext(ctx, episodeCoverageQuery,
		animeID, animeID, animeID, animeID).Scan(&stored, &incomplete, &status, &expected); err != nil {
		return true
	}
	if stored == 0 {
		return true
	}

	airing := status == "RELEASING" || status == "NOT_YET_RELEASED"
	if incomplete == 0 && stored >= expected && !airing {
		return false
	}
	if maxAge <= 0 {
		return true
	}

	at, err := s.SourceRefreshedAt(ctx, episodesKey(animeID))
	return err != nil || at.IsZero() || time.Since(at) >= maxAge
}

// StillGaps reports which episodes have no image.
func (s *Store) StillGaps(ctx context.Context, animeID int) (map[int]bool, error) {
	rows, err := s.r.QueryContext(ctx, `
		SELECT number FROM episode
		WHERE anime_id = ? AND number IS NOT NULL AND (still_url IS NULL OR still_url = '')`, animeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	missing := map[int]bool{}
	for rows.Next() {
		var number int
		if err := rows.Scan(&number); err != nil {
			return nil, err
		}
		missing[number] = true
	}
	return missing, rows.Err()
}

// SetEpisodeStills fills in images only where none exist: the per-episode
// source is the better one, and a streaming thumbnail should never displace it.
func (s *Store) SetEpisodeStills(ctx context.Context, animeID int, stills map[int]string) (int, error) {
	if len(stills) == 0 {
		return 0, nil
	}

	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		UPDATE episode SET still_url = ?
		WHERE anime_id = ? AND number = ? AND (still_url IS NULL OR still_url = '')`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	var n int
	for number, url := range stills {
		res, err := stmt.ExecContext(ctx, url, animeID, number)
		if err != nil {
			return 0, err
		}
		if rows, _ := res.RowsAffected(); rows > 0 {
			n++
		}
	}
	return n, tx.Commit()
}

// SaveDerivedEpisodes records episodes evidenced by releases rather than by a
// metadata source. They carry no titles, so a later real fetch fills them in
// without conflict.
func (s *Store) SaveDerivedEpisodes(ctx context.Context, animeID int, numbers []int) (int, error) {
	if err := s.MarkSource(ctx, derivedKey(animeID), len(numbers)); err != nil {
		return 0, err
	}
	if len(numbers) == 0 {
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
		INSERT INTO episode (anime_id, ep_key, number, is_special)
		VALUES (?,?,?,0)
		ON CONFLICT(anime_id, ep_key) DO NOTHING`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	var saved int
	for _, n := range numbers {
		if n <= 0 || n > 5000 {
			continue
		}
		if _, err := stmt.ExecContext(ctx, animeID, strconv.Itoa(n), n); err != nil {
			return 0, err
		}
		saved++
	}
	return saved, tx.Commit()
}

// EpisodesDerived reports that releases have already been searched for this
// show today. The search costs seconds against every indexer.
func (s *Store) EpisodesDerived(ctx context.Context, animeID int) bool {
	at, err := s.SourceRefreshedAt(ctx, derivedKey(animeID))
	return err == nil && !at.IsZero() && time.Since(at) < 24*time.Hour
}

func derivedKey(animeID int) string {
	return "derived:" + strconv.Itoa(animeID)
}

func (s *Store) MarkEpisodesFetched(ctx context.Context, animeID, count int) error {
	return s.MarkSource(ctx, episodesKey(animeID), count)
}

func episodesKey(animeID int) string {
	return "episodes:" + strconv.Itoa(animeID)
}

// FlagsFetched records the attempt, not the result. A show with no filler and
// no recaps returns nothing to store, and inferring "not fetched" from an
// absence of rows restarts the crawl on every page load.
func (s *Store) FlagsFetched(ctx context.Context, malID int) bool {
	if malID == 0 {
		return true
	}
	at, err := s.SourceRefreshedAt(ctx, flagsKey(malID))
	return err == nil && !at.IsZero()
}

func (s *Store) MarkFlagsFetched(ctx context.Context, malID int) error {
	return s.MarkSource(ctx, flagsKey(malID), 1)
}

func flagsKey(malID int) string {
	return "flags:" + strconv.Itoa(malID)
}
