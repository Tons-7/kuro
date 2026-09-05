package library

import (
	"context"
	"log/slog"
	"regexp"
	"strconv"
	"sync"
	"time"

	"kuro/internal/anilist"
	"kuro/internal/metadata"
	"kuro/internal/store"
)

// Enricher fills in what AniList does not carry: per-episode titles and
// stills, filler classification, and opening/ending timestamps.
type Enricher struct {
	store *store.Store
	meta  *metadata.Client
	ani   *anilist.Client
	log   *slog.Logger

	mu       sync.Mutex
	fetching map[int]struct{}
	cast     map[int]castEntry
	extra    map[int]extraEntry
	// One page lists every show the filler site covers, so it is worth keeping.
	fillerShows   map[string]string
	fillerShowsAt time.Time
}

func NewEnricher(s *store.Store, m *metadata.Client, log *slog.Logger) *Enricher {
	return &Enricher{
		store: s, meta: m, log: log,
		fetching: map[int]struct{}{},
		cast:     map[int]castEntry{},
		extra:    map[int]extraEntry{},
	}
}

// WithAniList adds a second source of episode stills: TheTVDB can be days
// behind broadcast, where a streaming listing usually has one on day one.
func (e *Enricher) WithAniList(c *anilist.Client) *Enricher {
	e.ani = c
	return e
}

// Anime reads a MyAnimeList record: anime AniList never catalogued have no
// other source of cover art or a synopsis.
func (e *Enricher) Anime(ctx context.Context, malID int) (metadata.AnimeDetail, error) {
	return e.meta.Anime(ctx, malID)
}

// A cast never changes after airing, so don't ask twice for the same show.
const castTTL = 12 * time.Hour

type castEntry struct {
	cast []metadata.Character
	at   time.Time
}

type extraEntry struct {
	extra metadata.Extra
	at    time.Time
}

// Extra resolves a show's themes, key staff and trailer. Cached like the cast:
// keyed by AniList id, resolved via MyAnimeList, kept since it never changes.
func (e *Enricher) Extra(ctx context.Context, animeID int) (metadata.Extra, error) {
	e.mu.Lock()
	if hit, ok := e.extra[animeID]; ok && time.Since(hit.at) < castTTL {
		e.mu.Unlock()
		return hit.extra, nil
	}
	e.mu.Unlock()

	malID, err := e.store.MalID(ctx, animeID)
	if err != nil || malID == 0 {
		return metadata.Extra{}, err
	}

	extra, err := e.meta.Extra(ctx, malID)
	if err != nil {
		return metadata.Extra{}, err
	}

	e.mu.Lock()
	e.extra[animeID] = extraEntry{extra: extra, at: time.Now()}
	e.mu.Unlock()
	return extra, nil
}

// Characters resolves the show's cast. The id is AniList's; the upstream is keyed
// by MyAnimeList, so it needs the mapping first.
func (e *Enricher) Characters(ctx context.Context, animeID int) ([]metadata.Character, error) {
	e.mu.Lock()
	if hit, ok := e.cast[animeID]; ok && time.Since(hit.at) < castTTL {
		e.mu.Unlock()
		return hit.cast, nil
	}
	e.mu.Unlock()

	malID, err := e.store.MalID(ctx, animeID)
	if err != nil || malID == 0 {
		return nil, err
	}

	cast, err := e.meta.Characters(ctx, malID)
	if err != nil {
		return nil, err
	}

	e.mu.Lock()
	e.cast[animeID] = castEntry{cast: cast, at: time.Now()}
	e.mu.Unlock()
	return cast, nil
}

// Only the ani.zip fetch is synchronous. Recap markers need a paginated crawl
// of up to twenty requests, which must never sit in front of a page load.
func (e *Enricher) Episodes(ctx context.Context, animeID int) (int, error) {
	var saved int

	if e.store.EpisodesStale(ctx, animeID, episodeRefresh) {
		mapping, err := e.meta.Episodes(ctx, animeID)
		if err != nil {
			return 0, err
		}
		if len(mapping.Episodes) > 0 {
			if err := e.store.EnsureAnime(ctx, animeID); err != nil {
				return 0, err
			}
			if saved, err = e.store.SaveEpisodes(ctx, animeID, mapping.Episodes); err != nil {
				return 0, err
			}
		}
		e.fillStills(ctx, animeID)

		// Recorded even on an empty result, else an unknown show refetches every load.
		if err := e.store.MarkEpisodesFetched(ctx, animeID, saved); err != nil {
			e.log.Warn("mark episodes fetched", "anime", animeID, "err", err)
		}
	}

	e.flagsAsync(animeID)
	return saved, nil
}

// Streaming listings label episodes "Episode 4 - The Perfect Crimson", and a
// few sites use the Japanese counter instead.
var episodeLabel = regexp.MustCompile(`(?i)(?:episode|ep\.?|第)\s*0*(\d+)`)

// fillStills covers the gap between an episode airing and TheTVDB having a
// frame from it.
func (e *Enricher) fillStills(ctx context.Context, animeID int) {
	if e.ani == nil {
		return
	}

	missing, err := e.store.StillGaps(ctx, animeID)
	if err != nil || len(missing) == 0 {
		return
	}

	listed, err := e.ani.StreamingEpisodes(ctx, animeID)
	if err != nil || len(listed) == 0 {
		return
	}

	found := map[int]string{}
	for _, ep := range listed {
		match := episodeLabel.FindStringSubmatch(ep.Title)
		if match == nil || ep.Thumbnail == "" {
			continue
		}
		if number, _ := strconv.Atoi(match[1]); missing[number] {
			found[number] = ep.Thumbnail
		}
	}

	n, err := e.store.SetEpisodeStills(ctx, animeID, found)
	if err != nil {
		e.log.Warn("episode stills", "anime", animeID, "err", err)
		return
	}
	if n > 0 {
		e.log.Info("episode stills filled from streaming listing", "anime", animeID, "count", n)
	}
}

func (e *Enricher) flagsAsync(animeID int) {
	malID, err := e.store.MalID(context.Background(), animeID)
	if err != nil || malID == 0 {
		return
	}

	e.mu.Lock()
	if _, busy := e.fetching[malID]; busy {
		e.mu.Unlock()
		return
	}
	if e.fetching == nil {
		e.fetching = map[int]struct{}{}
	}
	e.fetching[malID] = struct{}{}
	e.mu.Unlock()

	go func() {
		defer func() {
			e.mu.Lock()
			delete(e.fetching, malID)
			e.mu.Unlock()
		}()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		e.flags(ctx, animeID, malID)

		// Outside flags: that stops once it has been run for a show, while a
		// currently-airing series grows new unclassified episodes every week.
		e.fillerGaps(ctx, animeID, malID)
	}()
}

// Recorded whatever the outcome: a show with no flags returns nothing, and
// without a marker every page load would restart the crawl.
func (e *Enricher) flags(ctx context.Context, animeID, malID int) {
	if e.store.FlagsFetched(ctx, malID) {
		return
	}

	found, err := e.meta.Flags(ctx, malID)
	if err != nil {
		e.log.Warn("episode flags", "anime", animeID, "err", err)
		return
	}

	recaps, err := e.store.SaveFlags(ctx, malID, found)
	if err != nil {
		e.log.Warn("save episode flags", "anime", animeID, "err", err)
		return
	}
	if err := e.store.MarkFlagsFetched(ctx, malID); err != nil {
		e.log.Warn("mark flags fetched", "err", err)
	}
	if recaps > 0 {
		e.log.Info("recap episodes found", "anime", animeID, "count", recaps)
	}
}

// fillerGaps tops up from the site the dataset is built from, only into real gaps:
// scraped HTML is the least trustworthy source, so it gets the least authority.
func (e *Enricher) fillerGaps(ctx context.Context, animeID, malID int) {
	missing, err := e.store.UnclassifiedEpisodes(ctx, animeID)
	if err != nil || missing == 0 {
		return
	}

	titles, err := e.store.SearchTitles(ctx, animeID)
	if err != nil {
		return
	}
	if english, err := e.store.EnglishTitle(ctx, animeID); err == nil && english != "" {
		titles = append(titles, english)
	}

	slug := e.fillerSlug(ctx, titles)
	if slug == "" {
		return
	}

	rows, err := e.meta.FillerSiteEpisodes(ctx, slug)
	if err != nil {
		e.log.Debug("filler site", "anime", animeID, "slug", slug, "err", err)
		return
	}

	filled, err := e.store.SaveFillerGaps(ctx, malID, rows)
	if err != nil {
		e.log.Warn("save filler gaps", "anime", animeID, "err", err)
		return
	}
	if filled > 0 {
		e.log.Info("filled filler gaps from the site",
			"anime", animeID, "slug", slug, "episodes", filled)
	}
}

// The index is one page listing every show, so it is fetched once and kept.
func (e *Enricher) fillerSlug(ctx context.Context, titles []string) string {
	e.mu.Lock()
	index, fresh := e.fillerShows, time.Since(e.fillerShowsAt) < fillerRefresh
	e.mu.Unlock()

	if !fresh || index == nil {
		found, err := e.meta.FillerSiteShows(ctx)
		if err != nil {
			e.log.Debug("filler site index", "err", err)
			return ""
		}
		e.mu.Lock()
		e.fillerShows, e.fillerShowsAt = found, time.Now()
		index = found
		e.mu.Unlock()
	}

	for _, title := range titles {
		if slug, ok := index[metadata.NormaliseTitle(title)]; ok {
			return slug
		}
	}
	return ""
}

// Skips are per episode and only useful just before playback, so they are
// fetched on demand rather than crawled.
func (e *Enricher) Skips(ctx context.Context, animeID, episode int, durationSeconds int) error {
	malID, err := e.store.MalID(ctx, animeID)
	if err != nil || malID == 0 {
		return err
	}
	// AniSkip matches on episode length to within ~15s, so a guess is worse
	// than none.
	if durationSeconds <= 0 {
		return nil
	}

	skips, err := e.meta.Skips(ctx, malID, episode, durationSeconds)
	if err != nil || len(skips) == 0 {
		return err
	}
	return e.store.SaveSkips(ctx, malID, episode, skips)
}

const (
	fillerRefresh = 7 * 24 * time.Hour
	seadexRefresh = 3 * 24 * time.Hour

	// Long enough to cap repeated opens at ~one request a day, short enough that a
	// still added the morning after broadcast shows by evening.
	episodeRefresh = 6 * time.Hour
)

// SeaDex is human-curated and changes slowly, so the whole database is
// mirrored on a timer and every lookup afterwards is local.
func (e *Enricher) SeaDex(ctx context.Context, force bool) (int, error) {
	if !force {
		at, err := e.store.SourceRefreshedAt(ctx, "seadex")
		if err == nil && !at.IsZero() && time.Since(at) < seadexRefresh {
			return 0, nil
		}
	}

	releases, err := e.meta.SeaDex(ctx)
	if err != nil {
		return 0, err
	}

	n, err := e.store.SaveSeaDex(ctx, releases)
	if err != nil {
		return 0, err
	}
	e.log.Info("seadex mirrored", "releases", n)
	return n, e.store.MarkSource(ctx, "seadex", n)
}

// Fillers loads one dataset covering every show, so it refreshes on a timer
// rather than per anime.
func (e *Enricher) Fillers(ctx context.Context, force bool) (int, error) {
	if !force {
		at, err := e.store.SourceRefreshedAt(ctx, "filler")
		if err == nil && !at.IsZero() && time.Since(at) < fillerRefresh {
			return 0, nil
		}
	}

	rows, err := e.meta.Fillers(ctx)
	if err != nil {
		return 0, err
	}

	n, err := e.store.SaveFillers(ctx, rows)
	if err != nil {
		return 0, err
	}
	e.log.Info("filler data refreshed", "episodes", n)
	return n, e.store.MarkSource(ctx, "filler", n)
}
