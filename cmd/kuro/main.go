package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"kuro/internal/anilist"
	"kuro/internal/config"
	"kuro/internal/corpus"
	"kuro/internal/db"
	"kuro/internal/deps"
	"kuro/internal/indexer"
	"kuro/internal/jobs"
	"kuro/internal/library"
	"kuro/internal/mal"
	"kuro/internal/metadata"
	"kuro/internal/player"
	"kuro/internal/proc"
	"kuro/internal/score"
	"kuro/internal/server"
	"kuro/internal/store"
	"kuro/internal/torrent"
	"kuro/internal/transcode"
	"kuro/internal/update"
)

// A window is unwanted on a headless box or when kuro runs as a service.
func windowWanted() bool {
	if v := strings.ToLower(os.Getenv("KURO_NO_WINDOW")); v == "1" || v == "true" {
		return false
	}
	for _, arg := range os.Args[1:] {
		if arg == "--no-window" || arg == "-no-window" {
			return false
		}
	}
	return true
}

func interval(st *store.Store, key string, fallback time.Duration) time.Duration {
	prefs, err := st.Prefs(context.Background(), 0)
	if err != nil {
		return fallback
	}
	if secs := prefs.Int(key); secs >= 60 {
		return time.Duration(secs) * time.Second
	}
	return fallback
}

// debounce runs fn once, wait after the first call of a burst.
func debounce(wait time.Duration, fn func()) func() {
	var pending atomic.Bool
	return func() {
		if pending.CompareAndSwap(false, true) {
			time.AfterFunc(wait, func() {
				pending.Store(false)
				fn()
			})
		}
	}
}

// Stream session ids are "<anime>-<episode>", built by the stream handler.
func parseStreamID(id string) (animeID, episode int, ok bool) {
	dash := strings.LastIndexByte(id, '-')
	if dash <= 0 {
		return 0, 0, false
	}
	animeID, err := strconv.Atoi(id[:dash])
	if err != nil {
		return 0, 0, false
	}
	episode, err = strconv.Atoi(id[dash+1:])
	if err != nil {
		return 0, 0, false
	}
	return animeID, episode, true
}

func anilistClientID(ctx context.Context, st *store.Store, cfg config.Config) string {
	if id, _ := st.Setting(ctx, "anilist.client_id"); id != "" {
		return id
	}
	return cfg.AniList.ClientID
}

// newMAL restores a stored connection. Tokens are refreshed in-flight, so the
// client persists each new pair itself rather than waiting for a sync to end.
func newMAL(ctx context.Context, cfg config.Config, st *store.Store, log *slog.Logger) *mal.Client {
	// Credentials entered in the app win; the config file remains a fallback
	// for an install that was set up before Settings could hold them.
	id, _ := st.Setting(ctx, "mal.client_id")
	secret, _ := st.Setting(ctx, "mal.client_secret")
	if id == "" {
		id, secret = cfg.MAL.ClientID, cfg.MAL.ClientSecret
	}

	client := mal.New(log,
		mal.WithCredentials(id, secret),
		mal.OnToken(func(t mal.Token) {
			err := st.SetSettings(context.Background(), map[string]string{
				"mal.token":         t.Access,
				"mal.refresh.token": t.Refresh,
				"mal.expires_at":    strconv.FormatInt(t.Expires.Unix(), 10),
			})
			if err != nil {
				log.Warn("persist mal token", "err", err)
			}
		}))

	access, _ := st.Setting(ctx, "mal.token")
	if access == "" {
		if id != "" {
			log.Info("myanimelist not connected", "connect", "http://"+cfg.Addr+"/api/mal/auth/login")
		}
		return client
	}

	refresh, _ := st.Setting(ctx, "mal.refresh.token")
	expires, _ := st.SettingInt(ctx, "mal.expires_at")
	client.SetToken(mal.Token{
		Access:  access,
		Refresh: refresh,
		Expires: time.Unix(int64(expires), 0),
	})

	name, _ := st.Setting(ctx, "mal.user_name")
	log.Info("myanimelist connected", "user", name)
	return client
}

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	// Also ended by an update handing over to the new binary.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Started by an update: the old process still holds the port.
	update.WaitFor(os.Args[1:], 30*time.Second)
	exe, _ := os.Executable()
	// A new version that dies before serving hands back to the previous one.
	serving := false
	if exe != "" && update.Handover(os.Args[1:]) {
		defer func() {
			if serving {
				return
			}
			if err := update.Rollback(exe); err != nil {
				log.Error("restore the previous version", "err", err)
				return
			}
			log.Warn("this version failed to start; the previous one is back")
			if err := update.Relaunch(exe); err != nil {
				log.Error("start the previous version", "err", err)
			}
		}()
	}
	log.Info("kuro", "version", update.Version)

	// A clean exit stops helpers, but closing the window or a kill skips that
	// and leaks rqbit/ffmpeg, so the OS is made responsible instead.
	if err := proc.KillChildrenOnExit(); err != nil {
		log.Warn("helpers may outlive an abrupt exit", "err", err)
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// Claimed first: a second launch must not touch the running one's state.
	claim, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		if !kuroAnswers(cfg.LocalURL()) {
			return err
		}
		log.Info("kuro is already running; showing it", "url", cfg.LocalURL())
		if windowWanted() {
			config.OpenApp(ctx, cfg.LocalURL())
		}
		return nil
	}
	warnSlowDisk(log, cfg.BinDir, cfg.CacheDir)

	conn, err := db.Open(cfg.DatabasePath())
	if err != nil {
		return err
	}
	defer conn.Close()

	if err := conn.Migrate(); err != nil {
		return err
	}
	log.Info("database ready", "path", cfg.DatabasePath())

	st := store.New(conn)
	al := anilist.New(log)
	al.SetArchive(st)

	token, err := st.EnsureSetting(ctx, "access.token", server.NewAccessToken())
	if err != nil {
		return err
	}

	switch token, _ := st.Setting(ctx, "anilist.token"); {
	case token != "":
		al.SetToken(token)
		name, _ := st.Setting(ctx, "anilist.user_name")
		if expires, _ := st.SettingInt(ctx, "anilist.expires_at"); expires > 0 {
			if left := time.Until(time.Unix(int64(expires), 0)); left < 30*24*time.Hour {
				log.Warn("anilist token expiring", "in", left.Round(time.Hour))
			}
		}
		log.Info("anilist connected", "user", name)
	case anilistClientID(ctx, st, cfg) == "":
		log.Warn("anilist not configured; search works, sync does not",
			"add", "Settings -> Trackers", "register", "https://anilist.co/settings/developer")
	default:
		log.Info("anilist not connected", "connect", "http://"+cfg.Addr+"/api/auth/login")
	}

	malClient := newMAL(ctx, cfg, st, log)

	// In the user's order: the first decides the record kept for a torrent
	// several carry. Cached: one episode is six queries per site.
	var sites, adultSites []indexer.Source
	for _, in := range cfg.Indexers {
		// Skipped, not fatal: kuro then starts and Setup names the bad block.
		src, err := indexer.Build(in.Type, in.URL, in.Adult)
		if err != nil {
			log.Warn("config.toml: skipping an [[indexer]] block", "err", err)
			continue
		}
		if in.Adult {
			adultSites = append(adultSites, indexer.NewCached(src))
		} else {
			sites = append(sites, indexer.NewCached(src))
		}
	}
	var sources indexer.Source
	if len(sites) > 0 {
		sources = indexer.Multi{Sources: sites}
		log.Info("release sources", "sites", len(sites), "adult", len(adultSites), "config", cfg.ConfigPath())
	} else {
		log.Warn("no release sources configured; nothing can be searched",
			"add", "[[indexer]] blocks", "config", cfg.ConfigPath())
	}
	if stray := cfg.StrayConfig(); stray != "" {
		log.Warn("config.toml.txt found; kuro reads only config.toml", "path", stray)
	}
	finder := library.NewFinder(st, sources, log)
	if len(adultSites) > 0 {
		finder = finder.WithAdultIndexer(indexer.Multi{Sources: adultSites})
	}
	enricher := library.NewEnricher(st, metadata.New(), log).WithAniList(al)
	ingester := library.NewIngester(st, corpus.NewFetcher(), al, log)

	importer := library.NewImporter(st, al, log)
	malSync := library.NewMALSync(st, malClient, log)
	sync := library.NewSync(st, al, log).WithMAL(malSync).WithImporter(importer)
	watcher := library.NewWatcher(st, finder, sources, log)
	scheduler := jobs.New(log)

	// The torrent engine is optional at startup: everything except playback
	// still works without it, and saying so beats failing to boot.
	supervisor := torrent.NewSupervisor(torrent.Options{
		Binary:      cfg.Tool("rqbit"),
		CacheDir:    cfg.CacheDir,
		APIAddr:     cfg.Torrent.APIAddr,
		UploadLimit: cfg.Torrent.UploadLimitBytes,
		ListenPort:  cfg.Torrent.ListenPort,
		PeerLimit:   cfg.Torrent.PeerLimit,
		DisableUPnP: !cfg.Torrent.UPnPEnabled(),
		SessionDir:  filepath.Join(cfg.CacheDir, library.SessionDirName),
	}, log)
	defer supervisor.Stop()

	// Wired up whether or not the engine is running: calls through the client
	// start it, so installing rqbit later needs no restart.
	torrents := supervisor.Client()
	if err := supervisor.Ensure(ctx); err != nil {
		log.Warn("torrent engine not running; it starts when something needs it", "err", err)
	}

	mpv := player.New(cfg.Tool("mpv"), "", log)
	vlcBinary := player.ResolveVLC(cfg.VLCPath())
	if want := cfg.VLCPath(); want != "" && vlcBinary == "" {
		log.Warn("vlc_path does not exist", "path", want)
	}
	prefetcher := library.NewPrefetcher(st, finder, torrents, log)
	relations := library.NewRelations(st, al, log)

	playback := library.NewPlayback(st, finder, torrents, mpv, cfg.CacheDir, log).
		WithSync(sync).
		WithEnricher(enricher).
		WithPrefetcher(prefetcher).
		WithProber(transcode.NewProber(cfg.Tool("ffprobe"))).
		WithRelations(relations).
		WithPlayer("vlc", player.NewVLC(vlcBinary, log))

	// Downloads run one at a time: parallel ones share the connection, so each
	// finishes later and the awaited one finishes last.
	downloader := library.NewDownloader(st, prefetcher, func(ctx context.Context, animeID int) score.Preferences {
		prefs, err := st.Prefs(ctx, animeID)
		if err != nil {
			return score.DefaultPreferences()
		}
		return library.Preferences(prefs)
	}, log)
	downloader.Quiet(ctx)
	go downloader.Run(ctx)

	// Auto-download joins the same queue rather than fetching on its own.
	watcher.WithDownloader(downloader)

	// A pin left behind by a crash would never be released, slowly
	// filling the cache with files that can never be evicted.
	if err := st.UnpinAll(ctx); err != nil {
		log.Warn("clear stale cache pins", "err", err)
	}

	// rqbit assigns torrent ids per session, so stored ids point elsewhere
	// after a crash; realign before a cache sweep deletes the wrong file.
	if live, err := torrents.Live(ctx); err != nil {
		log.Warn("list torrents for reconcile", "err", err)
	} else if matched, orphaned, err := st.ReconcileTorrents(ctx, live); err != nil {
		log.Warn("reconcile torrents", "err", err)
	} else if matched+orphaned > 0 {
		log.Info("torrents reconciled", "matched", matched, "orphaned", orphaned)
	}
	cache := library.NewCache(st, torrents, cfg.CacheDir, log)
	playback.WithCache(cache)
	scheduler.Add(jobs.Job{
		Name: "cache-sweep", Every: 2 * time.Minute,
		Run: func(ctx context.Context) error { _, err := cache.Sweep(ctx); return err },
	})

	scheduler.Add(jobs.Job{
		Name: "anilist-sync", Every: interval(st, "sync.poll_seconds", 15*time.Minute),
		Run: func(ctx context.Context) error { _, err := sync.Run(ctx); return err },
	})
	// On start too: opened to watch tonight's episode, the bell should already
	// know about it rather than half an hour later.
	scheduler.Add(jobs.Job{
		Name: "release-watch", Every: interval(st, "notify.poll_seconds", 30*time.Minute), OnStart: true,
		Run: func(ctx context.Context) error { _, err := watcher.Poll(ctx); return err },
	})
	// Whole-database mirrors on long timers: the startup run is almost always
	// a no-op and never blocks the server coming up.
	scheduler.Add(jobs.Job{
		Name: "filler-data", Every: 24 * time.Hour, OnStart: true,
		Run: func(ctx context.Context) error { _, err := enricher.Fillers(ctx, false); return err },
	})
	scheduler.Add(jobs.Job{
		Name: "seadex", Every: 24 * time.Hour, OnStart: true,
		Run: func(ctx context.Context) error { _, err := enricher.SeaDex(ctx, false); return err },
	})
	// MAL access tokens last an hour. The client renews them in-flight, but a
	// regular push also keeps the refresh token from going stale unused. Push
	// first: the pull treats what differs from the last push as a site edit.
	scheduler.Add(jobs.Job{
		Name: "mal-sync", Every: interval(st, "sync.poll_seconds", 15*time.Minute),
		Run: func(ctx context.Context) error {
			if _, err := malSync.Run(ctx); err != nil {
				return err
			}
			_, err := malSync.Pull(ctx)
			return err
		},
	})

	var updater *update.Updater
	if exe != "" && update.Version != "dev" {
		updater = update.New(st, exe, filepath.Join(cfg.CacheDir, "update"), log).
			WithRestart(cancel)
		scheduler.Add(jobs.Job{
			Name: "update-check", Every: 6 * time.Hour, OnStart: true,
			Run: func(ctx context.Context) error { _, err := updater.Check(ctx); return err },
		})
	}

	streams := transcode.NewManager(
		cfg.Tool("ffmpeg"), cfg.Tool("ffprobe"),
		cfg.CacheDir, "libx264", log)
	if _, err := streams.Purge(); err != nil {
		log.Warn("purge transcode output", "err", err)
	}
	// A GPU encoder is preferred when one actually works here: it encodes 1080p
	// far faster than realtime. Probing takes trial encodes, so it runs after
	// the window opens rather than before.
	detectEncoder := func(ctx context.Context) {
		encoder := transcode.DetectEncoder(ctx, cfg.Tool("ffmpeg"), log)
		streams.SetEncoder(encoder)
		// Without a real-time hardware encoder, HEVC/10-bit releases stall on
		// seeks, so the finder prefers a directly-playable one.
		finder.WithHardwareTranscode(transcode.IsHardwareEncoder(encoder))
		log.Info("video encoder", "encoder", encoder)
	}
	go detectEncoder(ctx)

	// Detection above had nothing to ask if ffmpeg was not installed yet, and
	// software encoding for the rest of the session is not what was chosen.
	depsManager := deps.New(cfg.BinDir, log)
	// The old engine ran on through the download; the next call starts the new one.
	depsManager.OnInstalling(func(name string) {
		if name == "rqbit" {
			supervisor.Stop()
		}
	})
	depsManager.OnInstalled(func(name string) {
		if name != "ffmpeg" {
			return
		}
		detect, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		detectEncoder(detect)
	})

	// Seeking into a part that has not downloaded should start fetching there
	// immediately, rather than after the encoder has opened the source.
	streams.OnSeek(func(sessionID string, fraction float64) {
		animeID, episode, ok := parseStreamID(sessionID)
		if !ok {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		hash, index, err := st.CachedFile(ctx, animeID, strconv.Itoa(episode))
		if err != nil || hash == "" {
			return
		}
		live, err := torrents.Live(ctx)
		if err != nil {
			return
		}
		id, ok := live[strings.ToLower(hash)]
		if !ok {
			return
		}
		if err := torrents.PrioritiseAt(ctx, id, index, fraction); err != nil {
			log.Debug("prioritise at seek", "torrent", id, "err", err)
		}
	})

	// A killed browser never tells the server it stopped watching, so the reaper
	// is the only signal left; without it the episode stays pinned as "playing"
	// and keeps downloading with nobody watching.
	// Set below; the reaper cannot fire before the server exists.
	var api *server.Server
	streams.OnIdle(func(sessionID string) {
		// Also the only signal that would ever release the queue's hold.
		downloader.Release(sessionID)
		if api != nil {
			api.SessionGone(sessionID)
		}

		animeID, episode, ok := parseStreamID(sessionID)
		if !ok {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		playback.Suspend(ctx, animeID, episode)
	})

	api = server.New(server.Deps{
		Config:     cfg,
		Streams:    streams,
		Subtitles:  transcode.NewSubtitles(cfg.Tool("ffmpeg")),
		Thumbs:     transcode.NewThumbnails(cfg.Tool("ffmpeg"), log),
		Store:      st,
		AniList:    al,
		Importer:   importer,
		Ingester:   ingester,
		Playback:   playback,
		Watcher:    watcher,
		Enricher:   enricher,
		Relations:  relations,
		Cache:      cache,
		Jobs:       scheduler,
		MAL:        malClient,
		MALSync:    malSync,
		Scanner:    library.NewScanner(st, log),
		Sync:       sync,
		Prefetch:   prefetcher,
		Downloader: downloader,
		Updater:    updater,
		Token:      token,
		Player:     mpv,
		Indexer:    sources,
		Finder:     finder,
		Deps:       depsManager,
		Log:        log,
	})
	// After api exists: the idle hook reads it.
	go streams.Reap(ctx)

	// The catalogue every mirror hangs off; without a job a new install had
	// nothing to search or randomise. Its sources decide staleness (a month for
	// the seed, a day for the rest), so this is a near-noop after the first daily launch.
	scheduler.Add(jobs.Job{
		Name: "corpus", Every: 24 * time.Hour, OnStart: true,
		Run: func(ctx context.Context) error {
			rep, err := ingester.Run(ctx, false)
			// Nothing arrived, so the index built below already matches the
			// corpus — rebuilding it would be 34,000 entries of wasted work.
			// A partial run still indexes what it saved.
			if rep.Seeded == 0 && rep.Fetched == 0 {
				return err
			}
			return errors.Join(err, api.RebuildIndex(ctx))
		},
	})
	// Shows first seen while browsing join the index, one rebuild per burst.
	importer.OnNewTitles = debounce(2*time.Minute, func() {
		if err := api.RebuildIndex(ctx); err != nil {
			log.Warn("rebuild match index", "err", err)
		}
	})
	// Saved shows still airing stay current though nobody opens them: each
	// broadcast moves the next episode on, and in time the status.
	scheduler.Add(jobs.Job{
		Name: "airing-refresh", Every: 15 * time.Minute, OnStart: true,
		Run: func(ctx context.Context) error { _, err := importer.Refresh(ctx, 150); return err },
	})
	scheduler.Start(ctx)

	// Around a second and a half over a full corpus, and only the local-file
	// scanner needs it, so the port opens first.
	go func() {
		if err := api.RebuildIndex(ctx); err != nil {
			log.Warn("match index unavailable", "err", err)
		}
	}()
	go api.WarmRankings(ctx)

	srv := &http.Server{
		Handler:           api.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	binder := &binder{srv: srv, log: log, claimed: claim}
	api.OnRebind(binder.bind)

	drained := make(chan struct{})
	go func() {
		defer close(drained)
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	}()

	// Listening beyond loopback (for phone access) opens the port to the network
	// with only the access token in front, so it is a deliberate, remembered choice.
	addr := cfg.Addr
	if open, _ := st.Setting(ctx, "access.lan"); open == "true" {
		addr = server.LANAddr(cfg.Addr)
	}
	if err := binder.bind(addr); err != nil {
		return err
	}
	// Serving: the previous binary is no longer needed as a way back.
	serving = true
	if exe != "" {
		update.Cleanup(exe)
	}
	if len(api.PairingURLs()) > 0 {
		go api.LogFirewall(ctx)
	}
	for _, url := range api.PairingURLs() {
		log.Info("reachable on this network", "url", url, "qr", "/api/access/qr.svg")
	}

	// Running the executable should show the app, not print a URL to paste.
	window := &config.Window{
		Profile: cfg.ProfileDir(),
		Maximized: func() bool {
			mode, _ := st.Setting(context.Background(), "window.mode")
			return mode == "maximized"
		},
	}
	if windowWanted() {
		go window.Open(ctx, cfg.LocalURL())
	}

	<-ctx.Done()
	// Requests still in flight would otherwise meet a closed database and a
	// killed torrent engine, which the deferred cleanup below does next.
	<-drained
	// Closed before this process ends, so a relaunch finds the profile free.
	window.Close()
	return nil
}

// binder serves the app and can move it to another address without a restart,
// so switching between loopback and network-wide is a runtime toggle.
type binder struct {
	srv *http.Server
	log *slog.Logger

	mu sync.Mutex
	ln net.Listener
	// claimed is the startup listener that kept a second launch out.
	claimed net.Listener
}

func (b *binder) bind(addr string) error {
	ln := b.takeClaim(addr)
	if ln == nil {
		var err error
		if ln, err = net.Listen("tcp", addr); err != nil {
			return err
		}
	}

	b.mu.Lock()
	previous := b.ln
	b.ln = ln
	b.mu.Unlock()

	// Closed after the new one is up, so there is no window where nothing is
	// listening — a page open in the browser keeps working across the switch.
	if previous != nil {
		previous.Close()
	}

	b.log.Info("listening", "addr", "http://"+ln.Addr().String())
	go func() {
		// The old listener closing is how a rebind ends the previous Serve, and
		// Shutdown is how the process ends both.
		if err := b.srv.Serve(ln); err != nil &&
			!errors.Is(err, net.ErrClosed) && !errors.Is(err, http.ErrServerClosed) {
			b.log.Error("serve", "addr", ln.Addr().String(), "err", err)
		}
	}()
	return nil
}

// takeClaim hands over the startup listener if it is for addr, else frees it.
func (b *binder) takeClaim(addr string) net.Listener {
	b.mu.Lock()
	c := b.claimed
	b.claimed = nil
	b.mu.Unlock()
	if c == nil {
		return nil
	}
	if c.Addr().String() == addr {
		return c
	}
	c.Close()
	return nil
}

// kuroAnswers tells a running kuro from another program holding the port.
func kuroAnswers(base string) bool {
	client := http.Client{Timeout: 2 * time.Second}
	res, err := client.Get(strings.TrimSuffix(base, "/") + "/api/health")
	if err != nil {
		return false
	}
	defer res.Body.Close()
	var body struct {
		Version string `json:"version"`
	}
	return res.StatusCode == http.StatusOK && json.NewDecoder(res.Body).Decode(&body) == nil && body.Version != ""
}

// warnSlowDisk warns when kuro is unpacked somewhere disk-bound work will crawl.
// On a network share or USB stick everything (downloads, torrent writes,
// transcoding) is slow at once, indistinguishable from kuro being broken.
func warnSlowDisk(log *slog.Logger, dirs ...string) {
	seen := map[string]bool{}
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		root := filepath.VolumeName(dir)
		if seen[root] {
			continue
		}
		seen[root] = true

		if d := proc.DriveOf(dir); d.Slow {
			log.Warn("kuro is running from a slow disk; downloads and playback will be slow",
				"path", dir, "disk", d.Kind,
				"fix", "move the kuro folder to an internal drive")
		}
	}
}
