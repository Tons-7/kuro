// Package server exposes the HTTP API. Handlers parse requests, delegate, and
// write JSON; they contain no SQL and no external API calls.
package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"kuro/internal/anilist"
	"kuro/internal/config"
	"kuro/internal/deps"
	"kuro/internal/indexer"
	"kuro/internal/jobs"
	"kuro/internal/library"
	"kuro/internal/mal"
	"kuro/internal/parse"
	"kuro/internal/store"
	"kuro/internal/transcode"
	"kuro/internal/update"
	"kuro/web"
)

type Server struct {
	cfg        config.Config
	store      *store.Store
	anilist    *anilist.Client
	importer   *library.Importer
	ingester   *library.Ingester
	finder     *library.Finder
	playback   *library.Playback
	watcher    *library.Watcher
	enricher   *library.Enricher
	relations  *library.Relations
	cache      *library.Cache
	streams    *transcode.Manager
	subtitles  *transcode.Subtitles
	thumbs     *transcode.Thumbnails
	jobs       *jobs.Scheduler
	mal        *mal.Client
	malSync    *library.MALSync
	scanner    *library.Scanner
	syncer     *library.Sync
	prefetch   *library.Prefetcher
	player     library.Player
	indexer    indexer.Source
	deps       *deps.Manager
	downloader *library.Downloader
	updater    *update.Updater
	log        *slog.Logger

	// Extracted embedded fonts, keyed by stream session. Populated in the
	// background so opening a stream does not wait on ffmpeg.
	fontsMu  sync.Mutex
	fonts    map[string][]transcode.FontFile
	fontJobs map[string]struct{}

	// Version lookups in flight, so a polling setup page cannot stack them.
	latestMu   sync.Mutex
	latestJobs map[string]time.Time

	// Shows whose episode list is being derived from releases.
	derivingMu sync.Mutex
	deriving   map[int]struct{}

	token string
	// Set by main, which owns the listener. Nil in tests and in any build that
	// does not serve, where switching networks is meaningless.
	rebind     func(addr string) error
	vocab      vocabulary
	index      indexHolder
	rising     risingCache
	refreshing atomic.Bool
	scanning   atomic.Bool
}

type Deps struct {
	Config     config.Config
	Store      *store.Store
	AniList    *anilist.Client
	Importer   *library.Importer
	Ingester   *library.Ingester
	Playback   *library.Playback
	Watcher    *library.Watcher
	Enricher   *library.Enricher
	Relations  *library.Relations
	Cache      *library.Cache
	Streams    *transcode.Manager
	Subtitles  *transcode.Subtitles
	Thumbs     *transcode.Thumbnails
	Jobs       *jobs.Scheduler
	MAL        *mal.Client
	MALSync    *library.MALSync
	Scanner    *library.Scanner
	Sync       *library.Sync
	Prefetch   *library.Prefetcher
	Player     library.Player
	Indexer    indexer.Source
	Finder     *library.Finder
	Deps       *deps.Manager
	Downloader *library.Downloader
	Updater    *update.Updater
	Token      string
	Log        *slog.Logger
}

func New(d Deps) *Server {
	return &Server{
		cfg: d.Config, store: d.Store, anilist: d.AniList,
		importer: d.Importer, ingester: d.Ingester,
		indexer: d.Indexer, log: d.Log,
		// Built once by the caller: a second one here quietly lacked whatever
		// the real one had been configured with.
		finder:     d.Finder,
		playback:   d.Playback,
		watcher:    d.Watcher,
		enricher:   d.Enricher,
		relations:  d.Relations,
		cache:      d.Cache,
		streams:    d.Streams,
		subtitles:  d.Subtitles,
		thumbs:     d.Thumbs,
		jobs:       d.Jobs,
		mal:        d.MAL,
		malSync:    d.MALSync,
		scanner:    d.Scanner,
		syncer:     d.Sync,
		prefetch:   d.Prefetch,
		player:     d.Player,
		deps:       d.Deps,
		downloader: d.Downloader,
		updater:    d.Updater,
		token:      d.Token,
		fonts:      map[string][]transcode.FontFile{},
		fontJobs:   map[string]struct{}{},
		deriving:   map[int]struct{}{},
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("GET /api/search", s.search)
	mux.HandleFunc("GET /api/search/local", s.searchLocal)
	mux.HandleFunc("GET /api/library", s.library)
	mux.HandleFunc("GET /api/library/counts", s.libraryCounts)
	mux.HandleFunc("GET /api/library/export", s.exportLibrary)
	mux.HandleFunc("POST /api/library/import", s.importLibrary)
	mux.HandleFunc("GET /api/episodes/next", s.nextEpisode)
	mux.HandleFunc("GET /api/continue", s.continueWatching)
	mux.HandleFunc("GET /api/torrents", s.torrents)
	mux.HandleFunc("GET /api/match", s.match)
	mux.HandleFunc("GET /api/franchise", s.franchise)
	mux.HandleFunc("GET /api/episodes", s.episodes)
	mux.HandleFunc("GET /api/episode/sources", s.episodeSources)
	mux.HandleFunc("POST /api/fillers/refresh", s.refreshFillers)
	mux.HandleFunc("POST /api/seadex/refresh", s.refreshSeaDex)
	mux.HandleFunc("GET /api/schedule", s.schedule)
	mux.HandleFunc("GET /api/discover", s.discover)
	mux.HandleFunc("GET /api/random", s.random)
	mux.HandleFunc("GET /api/anime/{id}", s.anime)
	mux.HandleFunc("GET /api/anime/{id}/characters", s.characters)
	mux.HandleFunc("GET /api/anime/{id}/extra", s.extra)
	mux.HandleFunc("GET /api/browse", s.browse)
	mux.HandleFunc("GET /api/filters", s.filters)
	mux.HandleFunc("GET /api/recommend", s.recommend)

	mux.HandleFunc("GET /api/local", s.localStats)
	mux.HandleFunc("GET /api/local/files", s.localFiles)
	mux.HandleFunc("POST /api/local/scan", s.localScan)
	mux.HandleFunc("POST /api/local/paths", s.setLibraryPaths)
	mux.HandleFunc("POST /api/local/assign", s.assignLocalFile)
	mux.HandleFunc("POST /api/local/forget", s.forgetMissing)
	mux.HandleFunc("GET /api/local/{id}/stream", s.localStream)
	mux.HandleFunc("GET /api/jobs", s.jobStatus)
	mux.HandleFunc("POST /api/jobs/{name}", s.runJob)
	mux.HandleFunc("GET /api/corpus", s.corpusStats)
	mux.HandleFunc("POST /api/corpus/refresh", s.corpusRefresh)
	mux.HandleFunc("GET /api/settings", s.settings)
	mux.HandleFunc("GET /api/trackers", s.trackers)
	mux.HandleFunc("POST /api/trackers", s.setTracker)
	mux.HandleFunc("GET /api/setup", s.setup)
	mux.HandleFunc("POST /api/setup/install/{name}", s.installComponent)
	mux.HandleFunc("GET /api/update", s.updateStatus)
	mux.HandleFunc("POST /api/update/check", s.updateCheck)
	mux.HandleFunc("POST /api/update/apply", s.updateApply)
	mux.HandleFunc("GET /api/auth/login", s.authLogin)
	mux.HandleFunc("POST /api/auth/logout", s.authLogout)
	mux.HandleFunc("GET /callback", s.authCallback)
	mux.HandleFunc("POST /api/sync", s.sync)
	mux.HandleFunc("POST /api/play", s.play)
	mux.HandleFunc("POST /api/stop", s.stopPlayback)
	mux.HandleFunc("POST /api/progress", s.progress)
	mux.HandleFunc("POST /api/prepare", s.prepare)
	mux.HandleFunc("GET /api/episode/skips", s.skips)
	mux.HandleFunc("POST /api/watched", s.setWatched)
	mux.HandleFunc("POST /api/status", s.setStatus)
	mux.HandleFunc("POST /api/score", s.setScore)
	mux.HandleFunc("POST /api/continue/dismiss", s.dismissResume)
	mux.HandleFunc("POST /api/download", s.download)
	mux.HandleFunc("POST /api/download/all", s.downloadAll)
	mux.HandleFunc("POST /api/download/episodes", s.downloadEpisodes)
	mux.HandleFunc("GET /api/download/queue", s.downloadQueue)
	mux.HandleFunc("DELETE /api/download/all/{anime}", s.cancelDownloadAll)
	mux.HandleFunc("GET /api/downloads", s.downloads)
	mux.HandleFunc("DELETE /api/downloads/{hash}", s.removeDownload)
	mux.HandleFunc("POST /api/downloads/{hash}/pause", s.pauseDownload)
	mux.HandleFunc("POST /api/downloads/{hash}/resume", s.resumeDownload)
	mux.HandleFunc("POST /api/downloads/{hash}/keep", s.keepDownload)
	mux.HandleFunc("POST /api/downloads/{hash}/unkeep", s.unkeepDownload)
	mux.HandleFunc("GET /api/downloads/{hash}/files", s.downloadFiles)
	mux.HandleFunc("POST /api/downloads/{hash}/files/{index}/keep", s.keepFile)
	mux.HandleFunc("POST /api/downloads/{hash}/files/{index}/unkeep", s.unkeepFile)
	mux.HandleFunc("POST /api/downloads/clear", s.clearDownloads)
	mux.HandleFunc("GET /api/cache", s.cacheStatus)
	mux.HandleFunc("POST /api/cache/sweep", s.sweepCache)
	mux.HandleFunc("POST /api/cache/orphans", s.cleanOrphans)

	mux.HandleFunc("POST /api/stream/open", s.streamOpen)
	mux.HandleFunc("GET /api/stream/{id}/playlist.m3u8", s.streamPlaylist)
	mux.HandleFunc("GET /api/stream/{id}/init.mp4", s.streamInit)
	mux.HandleFunc("GET /api/stream/{id}/{segment}", s.streamSegment)
	mux.HandleFunc("GET /api/stream/{id}/subtitle/{track}", s.streamSubtitle)
	mux.HandleFunc("GET /api/stream/{id}/fonts", s.streamFonts)
	mux.HandleFunc("GET /api/stream/{id}/thumbnails", s.streamThumbnails)
	mux.HandleFunc("GET /api/stream/{id}/thumbs.jpg", s.streamThumbSheet)
	mux.HandleFunc("GET /api/stream/{id}/health", s.streamHealth)
	mux.HandleFunc("GET /api/stream/{id}/font/{font}", s.streamFont)
	mux.HandleFunc("POST /api/stream/{id}/audio", s.streamAudio)
	mux.HandleFunc("DELETE /api/stream/{id}", s.streamClose)

	mux.HandleFunc("GET /api/prefs", s.getPrefs)
	mux.HandleFunc("POST /api/prefs", s.setPref)
	mux.HandleFunc("GET /api/bookmarks", s.bookmarks)
	mux.HandleFunc("POST /api/bookmarks", s.setBookmark)
	mux.HandleFunc("GET /api/recent", s.recent)
	mux.HandleFunc("GET /api/history", s.history)
	mux.HandleFunc("GET /api/history/stats", s.watchStats)
	mux.HandleFunc("POST /api/history/forget", s.forgetHistory)
	mux.HandleFunc("GET /api/notifications", s.notifications)
	mux.HandleFunc("POST /api/notifications/read", s.markRead)
	mux.HandleFunc("DELETE /api/notifications", s.deleteNotification)
	mux.HandleFunc("POST /api/follow", s.setFollow)
	mux.HandleFunc("POST /api/releases/check", s.checkReleases)

	mux.HandleFunc("GET /api/mal", s.malStatus)
	mux.HandleFunc("GET /api/mal/auth/login", s.malLogin)
	mux.HandleFunc("POST /api/mal/auth/logout", s.malLogout)
	mux.HandleFunc("GET /mal/callback", s.malCallback)
	mux.HandleFunc("POST /api/mal/import", s.malImport)
	mux.HandleFunc("POST /api/mal/favourites/import", s.malFavouritesImport)
	mux.HandleFunc("POST /api/mal/sync", s.malPush)

	mux.HandleFunc("GET /api/access", s.access)
	mux.HandleFunc("GET /api/access/qr.svg", s.accessQR)
	mux.HandleFunc("POST /api/access/rotate", s.rotateAccess)
	mux.HandleFunc("POST /api/access/network", s.setAccessNetwork)

	app := apiOnly()
	if assets, ok := web.FS(); ok {
		app = spa(assets)
	}

	// Chosen by prefix, not a "/" mux pattern: a catch-all there would shadow every
	// route, turning wrong-method 405s into 404s and handing the SPA to JSON callers.
	root := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			mux.ServeHTTP(w, r)
			return
		}
		// Non-API routes the mux does know, such as the OAuth callbacks.
		if _, pattern := mux.Handler(r); pattern != "" {
			mux.ServeHTTP(w, r)
			return
		}
		app.ServeHTTP(w, r)
	})
	return s.logging(s.guard(root))
}

func (s *Server) continueWatching(w http.ResponseWriter, r *http.Request) {
	page, err := s.store.ContinueWatching(r.Context(), store.ParsePaging(r.URL.Query(), 20, 100))
	if err != nil {
		s.fail(w, "continue watching", err)
		return
	}
	send(w, http.StatusOK, page)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	count, err := s.store.CountSettings(r.Context())
	send(w, http.StatusOK, map[string]any{
		"ok":       err == nil,
		"settings": count,
		"anilist":  s.anilist.Authenticated(),
		"version":  update.Version,
	})
}

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if len(q) < 3 {
		send(w, http.StatusOK, map[string]any{"results": []any{}})
		return
	}

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	results, err := s.anilist.Search(r.Context(), q, limit)
	if err != nil {
		s.fail(w, "search", err)
		return
	}
	send(w, http.StatusOK, map[string]any{"results": results})
}

func (s *Server) library(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, err := s.store.Library(r.Context(), store.LibraryFilter{
		Status: q.Get("status"), Query: q.Get("q"), Sort: q.Get("sort"),
	}, store.ParsePaging(q, 60, 200))
	if err != nil {
		s.fail(w, "library", err)
		return
	}
	send(w, http.StatusOK, page)
}

func (s *Server) libraryCounts(w http.ResponseWriter, r *http.Request) {
	counts, err := s.store.LibraryCounts(r.Context())
	if err != nil {
		s.fail(w, "library counts", err)
		return
	}
	send(w, http.StatusOK, counts)
}

// searchLocal answers from the shows kuro already holds — the list and anything
// opened — so typing gets results at once and offline. AniList is the slow,
// rate-limited path behind it.
func (s *Server) searchLocal(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	results, err := s.store.SearchLocal(r.Context(), r.URL.Query().Get("q"), limit)
	if err != nil {
		s.fail(w, "search", err)
		return
	}
	titles := s.store.TitleMode(r.Context(), 0)
	for i := range results {
		results[i].Title = store.PickTitle(titles, results[i].Romaji, results[i].English)
	}
	send(w, http.StatusOK, map[string]any{"results": results})
}

// nextEpisode is what plays after an episode, honouring the show's filler rule.
func (s *Server) nextEpisode(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(r.URL.Query().Get("id"))
	after, _ := strconv.Atoi(r.URL.Query().Get("after"))
	if id == 0 || after < 0 {
		send(w, http.StatusBadRequest, map[string]any{"error": "id and after are required"})
		return
	}
	next, err := s.store.NextEpisode(r.Context(), id, after)
	if err != nil {
		s.fail(w, "next episode", err)
		return
	}
	send(w, http.StatusOK, map[string]any{"next": next})
}

func (s *Server) torrents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("q") == "" {
		send(w, http.StatusBadRequest, map[string]any{"error": "q is required"})
		return
	}

	filter := indexer.FilterNone
	if v, err := strconv.Atoi(q.Get("filter")); err == nil {
		filter = indexer.Filter(v)
	}
	limit, _ := strconv.Atoi(q.Get("limit"))

	if s.indexer == nil {
		send(w, http.StatusServiceUnavailable, map[string]any{"error": library.ErrNoIndexers.Error()})
		return
	}
	results, err := s.indexer.Search(r.Context(), indexer.Query{
		Text:     q.Get("q"),
		Category: q.Get("category"),
		Filter:   filter,
		Limit:    limit,
	})
	if err != nil {
		s.fail(w, "torrents", err)
		return
	}

	type item struct {
		indexer.Torrent
		Magnet string        `json:"magnet"`
		Parsed parse.Release `json:"parsed"`
	}
	out := make([]item, 0, len(results))
	for _, t := range results {
		out = append(out, item{Torrent: t, Magnet: t.Magnet(), Parsed: parse.Parse(t.Title)})
	}
	send(w, http.StatusOK, map[string]any{"results": out, "count": len(out)})
}

func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	values, err := s.store.Settings(r.Context())
	if err != nil {
		s.fail(w, "settings", err)
		return
	}
	send(w, http.StatusOK, values)
}

func (s *Server) fail(w http.ResponseWriter, op string, err error) {
	s.log.Error(op, "err", err)
	send(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
}

func (s *Server) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		s.log.Debug("request", "method", r.Method, "path", r.URL.Path, "took", time.Since(start))
	})
}

func send(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	// None of this is worth a second: a stale list is what makes a toggle look
	// like it did nothing.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}
