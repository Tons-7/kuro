package store

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"
)

// Defaults are the source of truth for what a setting means. The database only
// stores what differs, so a key added in a later version still has a value.
var Defaults = map[string]string{
	// Everything automatic is off by default: an unwanted skip or auto-advance is
	// more annoying than pressing a button.
	// The browser player works everywhere; mpv is desktop only, so it opts in.
	"playback.player":      "browser",
	"playback.autoplay":    "false",
	"playback.autoskip_op": "false",
	"playback.autoskip_ed": "false",
	"playback.autonext":    "false",
	// Auto-next and prefetch step over filler and recap episodes.
	"playback.skip_filler":  "false",
	"playback.anime4k":      "false",
	"playback.anime4k_mode": "A",
	"playback.anime4k_size": "M",
	// Resolve the next episode's release (a search, not a download) while this one
	// plays, so pressing next doesn't wait on the indexers.
	"playback.prepare_next": "true",

	"audio.prefer": "sub",

	// Subtitle languages, best first. A release in none is ranked down, not
	// refused (sometimes it's the only one). Same order decides which track plays.
	"subtitle.languages": `["en"]`,

	// What to show over an English dub. "full" is the safe default (a rewritten
	// dub script won't match the subtitles); "signs" shows only text and lyrics.
	"subtitle.on_dub": "full",

	"display.titles": TitleEnglish,

	"quality.ladder":         `["1080p:BD:HEVC:10","1080p:BD","1080p:WEB","720p"]`,
	"quality.max_auto_bytes": "3221225472",
	"quality.allow_hi10p":    "false",
	// Release groups to favour, as a JSON array; a match earns a scoring bonus.
	"release.prefer_groups": `[]`,

	"cache.budget_bytes":  "5368709120",
	"cache.prefetch_next": "false",
	// On: an episode keeps downloading after the player closes, so returning to it
	// (and its second-half subtitles) needs no swarm.
	"cache.prefetch_full": "true",
	// off | now (gone once watched) | keep2 (gone once the two after it are watched
	// too, so going back one is still free).
	"cache.autodelete": "off",
	// Kept downloads are never evicted, so this is their only automatic cleanup.
	"cache.autodelete_downloads": "false",

	"sync.progress_at":  "0.9",
	"sync.poll_seconds": "900",
	"sync.add_missing":  "true",

	"notify.enabled":      "true",
	"notify.releases":     "sub",
	"notify.poll_seconds": "1800",

	"autodownload.enabled": "false",
}

// Anime4K ships six shader chains for different source quality, and five CNN
// sizes where each step doubles GPU cost.
var (
	Anime4KModes = []string{"A", "B", "C", "A+A", "B+B", "C+A"}
	Anime4KSizes = []string{"S", "M", "L", "VL", "UL"}
	AudioPrefs   = []string{"sub", "dub", "either"}
	AutoDeletes  = []string{"off", "now", "keep2"}
	Players      = []string{"mpv", "browser"}
	TitleModes   = []string{TitleRomaji, TitleEnglish}
)

// Prefs resolves a setting for one anime: a per-show override if there is one,
// otherwise the global value, otherwise the compiled-in default.
type Prefs struct {
	values map[string]string
}

func (p Prefs) String(key string) string {
	if v, ok := p.values[key]; ok && v != "" {
		return v
	}
	return Defaults[key]
}

func (p Prefs) Bool(key string) bool { return p.String(key) == "true" }

func (p Prefs) Int(key string) int {
	n, _ := strconv.ParseInt(p.String(key), 10, 64)
	return int(n)
}

func (p Prefs) Int64(key string) int64 {
	n, _ := strconv.ParseInt(p.String(key), 10, 64)
	return n
}

func (p Prefs) Float(key string) float64 {
	f, _ := strconv.ParseFloat(p.String(key), 64)
	return f
}

func (p Prefs) All() map[string]string {
	out := make(map[string]string, len(Defaults))
	for k, v := range Defaults {
		out[k] = v
	}
	for k, v := range p.values {
		if v != "" {
			out[k] = v
		}
	}
	return out
}

func (s *Store) Prefs(ctx context.Context, animeID int) (Prefs, error) {
	values, err := s.Settings(ctx)
	if err != nil {
		return Prefs{}, err
	}

	if animeID != 0 {
		rows, err := s.r.QueryContext(ctx,
			`SELECT key, value FROM anime_pref WHERE anime_id = ?`, animeID)
		if err != nil {
			return Prefs{}, err
		}
		defer rows.Close()

		for rows.Next() {
			var k, v string
			if err := rows.Scan(&k, &v); err != nil {
				return Prefs{}, err
			}
			values[k] = v
		}
		if err := rows.Err(); err != nil {
			return Prefs{}, err
		}
	}
	return Prefs{values: values}, nil
}

// EnsureAnime creates the row other tables key on, from the corpus where it is
// known. Several tables reference anime(id), so without it playing anything the
// catalogue has not imported fails a foreign key.
func (s *Store) EnsureAnime(ctx context.Context, animeID int) error {
	return ensureAnime(ctx, s.w, animeID)
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func ensureAnime(ctx context.Context, db execer, animeID int) error {
	if animeID == 0 {
		return nil
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO anime (id, mal_id, title_romaji, format, episode_count, season_year, synced_at)
		SELECT c.anime_id, c.mal_id,
		       coalesce((SELECT text FROM title
		                 WHERE anime_id = c.anime_id
		                 ORDER BY (kind != 'primary'), id LIMIT 1), 'Unknown'),
		       c.kind, c.episodes, c.year, 0
		FROM corpus_anime c
		WHERE c.anime_id = ?
		ON CONFLICT(id) DO NOTHING`, animeID)
	if err != nil {
		return err
	}

	// A placeholder for anything the corpus has never seen, so the foreign key
	// holds. The real title arrives whenever the anime is next hydrated.
	_, err = db.ExecContext(ctx, `
		INSERT INTO anime (id, title_romaji, synced_at) VALUES (?, 'Unknown', 0)
		ON CONFLICT(id) DO NOTHING`, animeID)
	return err
}

// Unhydrated reports which of the ids are placeholders or missing, so a
// caller with a metadata source can fill them in.
func (s *Store) Unhydrated(ctx context.Context, ids []int) ([]int, error) {
	var out []int
	for _, id := range ids {
		var synced int64
		err := s.r.QueryRowContext(ctx,
			`SELECT synced_at FROM anime WHERE id = ?`, id).Scan(&synced)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && synced == 0) {
			out = append(out, id)
			continue
		}
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Store) SetAnimePref(ctx context.Context, animeID int, key, value string) error {
	if value == "" {
		_, err := s.w.ExecContext(ctx,
			`DELETE FROM anime_pref WHERE anime_id = ? AND key = ?`, animeID, key)
		return err
	}
	if err := s.EnsureAnime(ctx, animeID); err != nil {
		return err
	}
	_, err := s.w.ExecContext(ctx,
		`INSERT INTO anime_pref (anime_id, key, value) VALUES (?,?,?)
		 ON CONFLICT(anime_id, key) DO UPDATE SET value = excluded.value`,
		animeID, key, value)
	return err
}

func (s *Store) AnimePrefs(ctx context.Context, animeID int) (map[string]string, error) {
	rows, err := s.r.QueryContext(ctx,
		`SELECT key, value FROM anime_pref WHERE anime_id = ?`, animeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

type Bookmark struct {
	Favourite bool   `json:"favourite"`
	Pinned    bool   `json:"pinned"`
	Hidden    bool   `json:"hidden"`
	Note      string `json:"note"`
}

func (s *Store) SetBookmark(ctx context.Context, animeID int, b Bookmark) error {
	if err := s.EnsureAnime(ctx, animeID); err != nil {
		return err
	}
	_, err := s.w.ExecContext(ctx, `
		INSERT INTO user_anime (anime_id, favourite, pinned, hidden, note, updated_at,
		                        favourite_synced, favourite_dirty)
		VALUES (?,?,?,?,?,?,0,?)
		ON CONFLICT(anime_id) DO UPDATE SET
		    favourite=excluded.favourite, pinned=excluded.pinned,
		    hidden=excluded.hidden, note=excluded.note, updated_at=excluded.updated_at,
		    favourite_dirty = CASE
		        WHEN excluded.favourite <> user_anime.favourite_synced THEN 1 ELSE 0 END`,
		animeID, boolInt(b.Favourite), boolInt(b.Pinned), boolInt(b.Hidden),
		nullable(b.Note), time.Now().Unix(), boolInt(b.Favourite))
	return err
}

// FavouriteChange is a favourite the tracker has not been told about.
type FavouriteChange struct {
	AnimeID   int
	Favourite bool
}

func (s *Store) DirtyFavourites(ctx context.Context) ([]FavouriteChange, error) {
	rows, err := s.r.QueryContext(ctx,
		`SELECT anime_id, favourite FROM user_anime WHERE favourite_dirty = 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []FavouriteChange
	for rows.Next() {
		var c FavouriteChange
		var fav int
		if err := rows.Scan(&c.AnimeID, &fav); err != nil {
			return nil, err
		}
		c.Favourite = fav == 1
		out = append(out, c)
	}
	return out, rows.Err()
}

// MarkFavouriteSynced records what the tracker now holds. A row changed again
// while the push was in flight stays dirty, so that edit goes up next time.
func (s *Store) MarkFavouriteSynced(ctx context.Context, animeID int, favourite bool) error {
	_, err := s.w.ExecContext(ctx, `
		UPDATE user_anime
		SET favourite_synced = ?,
		    favourite_dirty = CASE WHEN favourite = ? THEN 0 ELSE 1 END
		WHERE anime_id = ?`,
		boolInt(favourite), boolInt(favourite), animeID)
	return err
}

// ApplyRemoteFavourites merges the tracker's list into the local one. Rows with
// unpushed changes are left alone, and rows already in step are not rewritten,
// so a sync never reorders the favourites page.
func (s *Store) ApplyRemoteFavourites(ctx context.Context, ids []int) (int, error) {
	remote := make(map[int]bool, len(ids))
	for _, id := range ids {
		remote[id] = true
	}

	rows, err := s.r.QueryContext(ctx,
		`SELECT anime_id, favourite FROM user_anime WHERE favourite_dirty = 0`)
	if err != nil {
		return 0, err
	}
	settled := map[int]bool{}
	for rows.Next() {
		var id, fav int
		if err := rows.Scan(&id, &fav); err != nil {
			rows.Close()
			return 0, err
		}
		settled[id] = fav == 1
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	now := time.Now().Unix()

	for id := range remote {
		if settled[id] {
			continue
		}
		if err := ensureAnime(ctx, tx, id); err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO user_anime (anime_id, favourite, updated_at,
			                        favourite_synced, favourite_dirty)
			VALUES (?,1,?,1,0)
			ON CONFLICT(anime_id) DO UPDATE SET
			    favourite = 1, favourite_synced = 1, updated_at = excluded.updated_at
			WHERE user_anime.favourite_dirty = 0`,
			id, now); err != nil {
			return 0, err
		}
	}
	for id, fav := range settled {
		if !fav || remote[id] {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE user_anime SET favourite = 0, favourite_synced = 0, updated_at = ?
			 WHERE anime_id = ? AND favourite_dirty = 0`,
			now, id); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(remote), nil
}

func (s *Store) Bookmark(ctx context.Context, animeID int) (Bookmark, error) {
	var b Bookmark
	var fav, pin, hid int
	var note *string
	err := s.r.QueryRowContext(ctx,
		`SELECT favourite, pinned, hidden, note FROM user_anime WHERE anime_id = ?`, animeID).
		Scan(&fav, &pin, &hid, &note)
	if errors.Is(err, sql.ErrNoRows) {
		return Bookmark{}, nil
	}
	if err != nil {
		return Bookmark{}, err
	}
	b.Favourite, b.Pinned, b.Hidden = fav == 1, pin == 1, hid == 1
	if note != nil {
		b.Note = *note
	}
	return b, nil
}

const bookmarkQuery = `
SELECT ` + libraryColumns + `
FROM user_anime u
JOIN anime a ON a.id = u.anime_id
LEFT JOIN list_entry e ON e.anime_id = a.id
` + latestPlayback + `
WHERE u.favourite = 1 AND u.hidden = 0
ORDER BY u.pinned DESC, u.updated_at DESC
LIMIT ? OFFSET ?`

func (s *Store) Bookmarks(ctx context.Context, p Paging) (Page[LibraryItem], error) {
	items, err := s.libraryRows(ctx, bookmarkQuery, p.PerPage+1, p.Offset())
	if err != nil {
		return Page[LibraryItem]{}, err
	}
	total, err := s.countRows(ctx,
		`SELECT count(*) FROM user_anime WHERE favourite = 1 AND hidden = 0`)
	if err != nil {
		return Page[LibraryItem]{}, err
	}
	return NewPage(items, p, total), nil
}

// RecentlyWatched is ordered by when playback last happened, regardless of
// whether the episode was finished.
const recentQuery = `
SELECT ` + libraryColumns + `
FROM anime a
JOIN playback p2 ON p2.anime_id = a.id
LEFT JOIN list_entry e ON e.anime_id = a.id
` + latestPlayback + `
GROUP BY a.id
ORDER BY max(p2.last_played_at) DESC
LIMIT ? OFFSET ?`

func (s *Store) RecentlyWatched(ctx context.Context, p Paging) (Page[LibraryItem], error) {
	items, err := s.libraryRows(ctx, recentQuery, p.PerPage+1, p.Offset())
	if err != nil {
		return Page[LibraryItem]{}, err
	}
	total, err := s.countRows(ctx, `SELECT count(DISTINCT anime_id) FROM playback`)
	if err != nil {
		return Page[LibraryItem]{}, err
	}
	return NewPage(items, p, total), nil
}
