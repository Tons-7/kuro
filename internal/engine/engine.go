// Package engine is kuro's torrent engine: anacrolix/torrent in process, behind
// a small loopback HTTP API (rqbit-compatible, so old sessions carry over).
// Streaming reads raise their pieces' priority and a piece's blocks come from
// many peers at once, so playback starts on the first bytes.
package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	alog "github.com/anacrolix/log"
	atorrent "github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
	"golang.org/x/time/rate"

	"kuro/internal/torrent"
)

type Options struct {
	// CacheDir holds the downloads, laid out as rqbit did: a single file at
	// CacheDir/<name>, a pack at CacheDir/<name>/<path>.
	CacheDir string
	// SessionDir keeps the torrent list, their metainfo and piece completion.
	SessionDir string
	// LegacySessions are rqbit sessions, tried in order; the first found is
	// imported once so its downloads carry over. One inside CacheDir is then
	// deleted; one elsewhere may still be rqbit's own and is left alone.
	LegacySessions []string

	ListenPort  int
	PeerLimit   int
	UploadLimit int
	DisableUPnP bool
}

// Engine implements torrent.Engine; the client it hands out talks to it over loopback.
type Engine struct {
	opts Options
	log  *slog.Logger

	listener net.Listener
	server   *http.Server
	client   *torrent.Client

	// saveMu: callers on several goroutines would share session.json.tmp.
	saveMu  sync.Mutex
	startMu sync.Mutex
	running bool

	mu        sync.Mutex
	cl        *atorrent.Client
	pieces    storage.PieceCompletion
	byID      map[int]*entry
	byHash    map[metainfo.Hash]*entry
	nextID    int
	inspected map[metainfo.Hash]*inspection
	closed    bool

	// Test hooks: adjust the client config; see each torrent as it is added.
	configure func(*atorrent.ClientConfig)
	onNew     func(*atorrent.Torrent)
}

type entry struct {
	id       int
	t        *atorrent.Torrent
	trackers []string
	// selected files by index; applied once the info is known.
	selected map[int]bool
	paused   bool
	// verifying: checking data already on disk; reported as "initializing".
	verifying bool
	err       string

	// Speed, sampled on each stats read.
	lastBytes int64
	lastAt    time.Time
	mibps     float64

	// For unstick: useful bytes at the last change, and when that was.
	useful     int64
	quietSince time.Time
}

// inspection is a torrent added only to read its file list. It is kept a few
// minutes, connected and downloading nothing, so the add that usually follows
// starts with its peers already there.
type inspection struct {
	t       *atorrent.Torrent
	expires time.Time
}

const inspectionTTL = 3 * time.Minute

func New(opts Options, log *slog.Logger) (*Engine, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	e := &Engine{
		opts:      opts,
		log:       log,
		listener:  l,
		byID:      map[int]*entry{},
		byHash:    map[metainfo.Hash]*entry{},
		nextID:    1,
		inspected: map[metainfo.Hash]*inspection{},
	}
	e.server = &http.Server{Handler: e.routes(), ReadHeaderTimeout: 10 * time.Second}
	go e.server.Serve(l)
	e.client = torrent.NewClient("http://" + l.Addr().String()).WithEngine(e)
	return e, nil
}

// Client is what the rest of kuro uses.
func (e *Engine) Client() *torrent.Client { return e.client }

func (e *Engine) Addr() string { return e.listener.Addr().String() }

// Ensure starts the engine on first use; a failed start is retried next call.
func (e *Engine) Ensure(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	e.startMu.Lock()
	defer e.startMu.Unlock()
	if e.running {
		return nil
	}
	if err := e.start(); err != nil {
		return fmt.Errorf("%w: %v", torrent.ErrUnavailable, err)
	}
	e.running = true
	return nil
}

func (e *Engine) start() error {
	e.mu.Lock()
	closed := e.closed
	e.mu.Unlock()
	if closed {
		return errors.New("engine stopped")
	}
	for _, dir := range []string{e.opts.CacheDir, e.opts.SessionDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	pieces, err := storage.NewBoltPieceCompletion(e.opts.SessionDir)
	if err != nil {
		return fmt.Errorf("piece completion: %w", err)
	}

	port := e.opts.ListenPort
	if port == 0 {
		port = 4240
	}
	cl, err := atorrent.NewClient(e.clientConfig(pieces, port))
	if err != nil {
		// Another client holds the port: a random one still works, just
		// without inbound peers from the forwarded port.
		e.log.Warn("peer port unavailable; using a random one", "port", port, "err", err)
		for range 5 {
			if cl, err = atorrent.NewClient(e.clientConfig(pieces, freePort())); err == nil {
				break
			}
		}
	}
	if err != nil {
		pieces.Close()
		return err
	}

	e.mu.Lock()
	e.cl, e.pieces = cl, pieces
	e.mu.Unlock()

	if err := e.restore(); err != nil {
		e.log.Warn("restore torrents", "err", err)
	}
	go e.janitor()
	e.log.Info("torrent engine started", "peer_port", cl.LocalPort(), "api", e.Addr())
	return nil
}

// freePort finds a port open for both TCP and UDP, which anacrolix binds
// together. Port 0 won't do on Windows: the OS hands out TCP ports in order,
// and a whole run of their UDP twins can sit in a reserved range.
func freePort() int {
	for range 20 {
		pc, err := net.ListenPacket("udp", ":0")
		if err != nil {
			continue
		}
		port := pc.LocalAddr().(*net.UDPAddr).Port
		l, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
		pc.Close()
		if err == nil {
			l.Close()
			return port
		}
	}
	return 0
}

// clientConfig is built per attempt: NewClient takes ownership of it.
func (e *Engine) clientConfig(pieces storage.PieceCompletion, port int) *atorrent.ClientConfig {
	cfg := atorrent.NewDefaultClientConfig()
	cfg.DataDir = e.opts.CacheDir
	cfg.DefaultStorage = files{base: e.opts.CacheDir, pieces: pieces}
	cfg.ListenPort = port
	if e.opts.PeerLimit > 0 {
		cfg.EstablishedConnsPerTorrent = e.opts.PeerLimit
	}
	if e.opts.UploadLimit > 0 {
		cfg.UploadRateLimiter = rate.NewLimiter(rate.Limit(e.opts.UploadLimit), 256<<10)
	}
	cfg.NoDefaultPortForwarding = e.opts.DisableUPnP
	cfg.Seed = true
	cfg.DisableWebtorrent = true
	cfg.Logger = alog.Default.WithFilterLevel(alog.Warning)
	cfg.Slogger = slog.New(warnOnly{e.log.Handler()})
	if e.configure != nil {
		e.configure(cfg)
	}
	return cfg
}

// Stop saves the session and closes everything; data stays on disk.
func (e *Engine) Stop() {
	// Waits out a start in progress, so its client is closed too.
	e.startMu.Lock()
	defer e.startMu.Unlock()
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return
	}
	e.closed = true
	cl, pieces := e.cl, e.pieces
	e.mu.Unlock()

	shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	e.server.Shutdown(shutdown)
	cancel()
	if cl != nil {
		e.saveSession()
		cl.Close()
	}
	if pieces != nil {
		pieces.Close()
	}
}

// Vars so tests need not wait.
var (
	janitorEvery = 5 * time.Second
	stallAfter   = 30 * time.Second
)

func (e *Engine) janitor() {
	tick := time.NewTicker(janitorEvery)
	defer tick.Stop()
	for range tick.C {
		e.mu.Lock()
		if e.closed {
			e.mu.Unlock()
			return
		}
		now := time.Now()
		for hash, in := range e.inspected {
			if now.After(in.expires) {
				delete(e.inspected, hash)
				if _, managed := e.byHash[hash]; !managed {
					in.t.Drop()
				}
			}
		}
		entries := make([]*entry, 0, len(e.byID))
		for _, en := range e.byID {
			entries = append(entries, en)
		}
		e.mu.Unlock()
		for _, en := range entries {
			e.unstick(en, now)
		}
	}
}

// unstick re-plans a torrent that wants data and has peers but received nothing
// for stallAfter. anacrolix re-plans only on events, so one missed around a
// pause stalls a single-seeder torrent for good. apply re-allows unless paused.
func (e *Engine) unstick(en *entry, now time.Time) {
	t := en.t
	if t.Info() == nil {
		return
	}
	st := t.Stats()
	useful := st.ConnStats.BytesReadUsefulData.Int64()

	e.mu.Lock()
	idle := !en.paused && !en.verifying && st.ActivePeers > 0 && useful == en.useful && wantsData(en)
	if !idle {
		en.useful, en.quietSince = useful, now
	}
	stalled := idle && now.Sub(en.quietSince) >= stallAfter
	if stalled {
		en.quietSince = now
	}
	e.mu.Unlock()

	if stalled {
		e.log.Debug("re-planning a stalled torrent", "hash", t.InfoHash().HexString())
		t.DisallowDataDownload()
		e.apply(en)
	}
}

// wantsData reports a selected file not yet complete. Callers hold e.mu.
func wantsData(en *entry) bool {
	for i, f := range en.t.Files() {
		if en.selected[i] && f.BytesCompleted() < f.Length() {
			return true
		}
	}
	return false
}

// warnOnly passes anacrolix's warnings and errors to kuro's log; below that it logs a line per peer event.
type warnOnly struct{ slog.Handler }

func (w warnOnly) Enabled(ctx context.Context, l slog.Level) bool {
	return l >= slog.LevelWarn && w.Handler.Enabled(ctx, l)
}

func (w warnOnly) WithAttrs(a []slog.Attr) slog.Handler { return warnOnly{w.Handler.WithAttrs(a)} }
func (w warnOnly) WithGroup(n string) slog.Handler      { return warnOnly{w.Handler.WithGroup(n)} }

var errNotFound = errors.New("torrent not found")

func (e *Engine) entryByID(id int) (*entry, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	en, ok := e.byID[id]
	if !ok {
		return nil, errNotFound
	}
	return en, nil
}

func sessionFile(dir string) string { return filepath.Join(dir, "session.json") }

func (e *Engine) added(t *atorrent.Torrent) {
	if e.onNew != nil {
		e.onNew(t)
	}
}
