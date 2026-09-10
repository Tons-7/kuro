package server

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"time"

	"kuro/internal/library"
	"kuro/internal/match"
	"kuro/internal/parse"
	"kuro/internal/score"
)

// The matcher index is rebuilt after ingestion and read on every lookup, so it
// is swapped behind a lock rather than mutated.
type indexHolder struct {
	mu sync.RWMutex
	ix *match.Index
}

func (h *indexHolder) get() *match.Index {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.ix
}

func (h *indexHolder) set(ix *match.Index) {
	h.mu.Lock()
	h.ix = ix
	h.mu.Unlock()
}

func (s *Server) corpusStats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.store.CorpusStats(r.Context())
	if err != nil {
		s.fail(w, "corpus stats", err)
		return
	}

	indexed := 0
	if ix := s.index.get(); ix != nil {
		indexed = ix.Len()
	}
	send(w, http.StatusOK, map[string]any{
		"anime": stats.Anime, "titles": stats.Titles,
		"dead": stats.Dead, "indexed": indexed,
	})
}

// Ingestion downloads tens of megabytes and can spend minutes inside AniList's
// rate limit, so it runs detached from the request.
func (s *Server) corpusRefresh(w http.ResponseWriter, r *http.Request) {
	force := r.URL.Query().Get("force") == "true"

	if !s.refreshing.CompareAndSwap(false, true) {
		send(w, http.StatusConflict, map[string]any{"error": "a refresh is already running"})
		return
	}

	go func() {
		defer s.refreshing.Store(false)

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
		defer cancel()

		report, err := s.ingester.Run(ctx, force)
		if err != nil {
			s.log.Error("corpus refresh", "err", err)
			return
		}
		s.log.Info("corpus refreshed", "seeded", report.Seeded,
			"discovered", report.Discovered, "titles", report.Titles)

		if err := s.RebuildIndex(ctx); err != nil {
			s.log.Error("rebuild index", "err", err)
		}
	}()

	send(w, http.StatusAccepted, map[string]any{"status": "started"})
}

// Episode data is fetched the first time a detail page is opened, so only
// shows the user actually looks at cost a request.
func (s *Server) episodes(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(r.URL.Query().Get("id"))
	if id == 0 {
		send(w, http.StatusBadRequest, map[string]any{"error": "id is required"})
		return
	}

	// The enricher decides internally what is missing: episode data and recap
	// markers come from different sources and go stale independently.
	if s.enricher != nil {
		if _, err := s.enricher.Episodes(r.Context(), id); err != nil {
			s.log.Warn("fetch episodes", "anime", id, "err", err)
		}
	}

	eps, err := s.store.Episodes(r.Context(), id)
	if err != nil {
		s.fail(w, "episodes", err)
		return
	}

	// A show reached only through the schedule has never been stored, so even
	// its episode count is unknown locally and the list comes back empty.
	if len(eps) == 0 && s.importer != nil {
		if _, err := s.importer.Hydrate(r.Context(), []int{id}); err != nil {
			s.log.Warn("hydrate anime", "anime", id, "err", err)
		} else if eps, err = s.store.Episodes(r.Context(), id); err != nil {
			s.fail(w, "episodes", err)
			return
		}
	}

	// Rows invented from an episode count are all this show has; releases are the
	// only real evidence, so search for them behind the response for the next load.
	if len(eps) > 0 && eps[0].Planned {
		s.deriveEpisodesAsync(id)
	}

	send(w, http.StatusOK, map[string]any{"items": eps, "count": len(eps)})
}

// deriveEpisodesAsync records which episodes releases exist for, once per show
// per day. Only titles nothing has catalogued reach this.
func (s *Server) deriveEpisodesAsync(animeID int) {
	if s.finder == nil || s.store.EpisodesDerived(context.Background(), animeID) {
		return
	}

	s.derivingMu.Lock()
	if _, busy := s.deriving[animeID]; busy {
		s.derivingMu.Unlock()
		return
	}
	s.deriving[animeID] = struct{}{}
	s.derivingMu.Unlock()

	go func() {
		defer func() {
			s.derivingMu.Lock()
			delete(s.deriving, animeID)
			s.derivingMu.Unlock()
		}()

		ctx, cancel := detached(5 * time.Minute)
		defer cancel()

		numbers, err := s.finder.EpisodeNumbers(ctx, animeID)
		if err != nil {
			s.log.Warn("derive episodes", "anime", animeID, "err", err)
			return
		}
		saved, err := s.store.SaveDerivedEpisodes(ctx, animeID, numbers)
		if err != nil {
			s.log.Warn("save derived episodes", "anime", animeID, "err", err)
			return
		}
		if saved > 0 {
			s.log.Info("episodes derived from releases", "anime", animeID, "count", saved)
		}
	}()
}

// cleanOrphans deletes downloads the torrent engine no longer knows about.
// Separate from the sweep because deleting by inference deserves an explicit ask.
func (s *Server) cleanOrphans(w http.ResponseWriter, r *http.Request) {
	if s.cache == nil {
		send(w, http.StatusServiceUnavailable, map[string]any{"error": "torrent engine unavailable"})
		return
	}

	files, bytes, err := s.cache.Orphans(r.Context())
	if err != nil {
		s.fail(w, "clean orphans", err)
		return
	}
	send(w, http.StatusOK, map[string]any{"removed": files, "freedBytes": bytes})
}

func (s *Server) refreshFillers(w http.ResponseWriter, r *http.Request) {
	if s.enricher == nil {
		send(w, http.StatusServiceUnavailable, map[string]any{"error": "enricher unavailable"})
		return
	}
	n, err := s.enricher.Fillers(r.Context(), r.URL.Query().Get("force") == "true")
	if err != nil {
		s.fail(w, "filler refresh", err)
		return
	}
	send(w, http.StatusOK, map[string]any{"episodes": n})
}

func (s *Server) refreshSeaDex(w http.ResponseWriter, r *http.Request) {
	if s.enricher == nil {
		send(w, http.StatusServiceUnavailable, map[string]any{"error": "enricher unavailable"})
		return
	}
	n, err := s.enricher.SeaDex(r.Context(), r.URL.Query().Get("force") == "true")
	if err != nil {
		s.fail(w, "seadex refresh", err)
		return
	}
	count, _ := s.store.SeaDexCount(r.Context())
	send(w, http.StatusOK, map[string]any{"fetched": n, "stored": count})
}

func (s *Server) franchise(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(r.URL.Query().Get("id"))
	if id == 0 {
		send(w, http.StatusBadRequest, map[string]any{"error": "id is required"})
		return
	}

	// The graph is walked the first time a show is opened; a standalone entry
	// is recorded as a franchise of one so it is not retried every visit.
	if s.relations != nil {
		if err := s.relations.Ensure(r.Context(), id); err != nil {
			s.log.Warn("fetch relations", "anime", id, "err", err)
		}
	}

	fr, err := s.store.Franchise(r.Context(), id)
	if err != nil {
		s.fail(w, "franchise", err)
		return
	}

	// Related entries are recorded as bare ids, so any never imported would
	// render as a blank row.
	var missing []int
	for _, season := range fr.Seasons {
		if season.Romaji == "" {
			missing = append(missing, season.ID)
		}
	}
	if len(missing) > 0 && s.importer != nil {
		if _, err := s.importer.Hydrate(r.Context(), missing); err != nil {
			s.log.Warn("hydrate franchise", "anime", id, "err", err)
		} else if fr, err = s.store.Franchise(r.Context(), id); err != nil {
			s.fail(w, "franchise", err)
			return
		}
	}

	related, err := s.store.Related(r.Context(), id)
	if err != nil {
		s.log.Warn("related entries", "anime", id, "err", err)
	}
	// Same as the seasons: an id never imported would render as a blank chip.
	var blank []int
	for _, e := range related {
		if e.Romaji == "" {
			blank = append(blank, e.ID)
		}
	}
	if len(blank) > 0 && s.importer != nil {
		if _, err := s.importer.Hydrate(r.Context(), blank); err != nil {
			s.log.Warn("hydrate related", "anime", id, "err", err)
		} else if related, err = s.store.Related(r.Context(), id); err != nil {
			s.fail(w, "related entries", err)
			return
		}
	}

	send(w, http.StatusOK, map[string]any{
		"rootId":  fr.RootID,
		"seasons": fr.Seasons,
		"related": related,
	})
}

// episodes finds releases for an anime the user already picked, so the season
// is known rather than inferred.
func (s *Server) episodeSources(w http.ResponseWriter, r *http.Request) {
	if s.finder == nil {
		send(w, http.StatusServiceUnavailable, map[string]any{
			"error": "release search unavailable",
		})
		return
	}

	id, _ := strconv.Atoi(r.URL.Query().Get("id"))
	episode, _ := strconv.Atoi(r.URL.Query().Get("episode"))
	if id == 0 {
		send(w, http.StatusBadRequest, map[string]any{"error": "id is required"})
		return
	}

	season, _ := strconv.Atoi(r.URL.Query().Get("season"))
	if season == 0 {
		season = 1
	}

	got, err := s.finder.Find(r.Context(), library.Request{
		AnimeID: id,
		Episode: episode,
		Season:  season,
		Prefs:   s.preferences(r.Context(), id),
	})
	if err != nil {
		s.fail(w, "episode sources", err)
		return
	}
	send(w, http.StatusOK, got)
}

// preferences resolves the settings for one show: a per-show override wins over
// the global, which is what playback already does. Zero means no show in mind.
func (s *Server) preferences(ctx context.Context, animeID int) score.Preferences {
	resolved, err := s.store.Prefs(ctx, animeID)
	if err != nil {
		s.log.Warn("read preferences", "anime", animeID, "err", err)
		return score.DefaultPreferences()
	}
	return library.Preferences(resolved)
}

func (s *Server) RebuildIndex(ctx context.Context) error {
	ix, err := s.store.BuildIndex(ctx)
	if err != nil {
		return err
	}
	s.index.set(ix)
	s.log.Info("match index built", "anime", ix.Len())
	return nil
}

// match resolves a release name the way the download path will, so a wrong
// pick can be reproduced without starting a torrent.
func (s *Server) match(w http.ResponseWriter, r *http.Request) {
	title := r.URL.Query().Get("title")
	if title == "" {
		send(w, http.StatusBadRequest, map[string]any{"error": "title is required"})
		return
	}

	ix := s.index.get()
	if ix == nil || ix.Len() == 0 {
		send(w, http.StatusPreconditionFailed, map[string]any{
			"error": "title corpus is empty; POST /api/corpus/refresh first",
		})
		return
	}

	rel := parse.Parse(title)
	q := match.Query{
		Title:   rel.Title,
		Season:  rel.Season,
		Episode: rel.Episode,
	}
	if year, _ := strconv.Atoi(r.URL.Query().Get("year")); year > 0 {
		q.Year = year
	}
	if boosts, err := s.store.MatchBoosts(r.Context()); err == nil {
		q.Boost = boosts
	}

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 5
	}

	best, ok := ix.Match(q)
	send(w, http.StatusOK, map[string]any{
		"parsed":     rel,
		"query":      q,
		"matched":    ok,
		"best":       best,
		"candidates": ix.Rank(q, limit),
	})
}
