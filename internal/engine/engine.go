// Package engine runs kuro's BitTorrent engine (internal/bt) behind a loopback API shaped like rqbit's.
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
	"golang.org/x/time/rate"
	"kuro/internal/bt"
	"kuro/internal/bt/metainfo"
	"kuro/internal/bt/storage"

	"kuro/internal/torrent"
)

type Options struct {
	// CacheDir holds downloads as rqbit did: CacheDir/<name>, or CacheDir/<name>/<path> for a pack.
	CacheDir string
	// SessionDir keeps the torrent list, their metainfo and piece completion.
	SessionDir string
	// LegacySessions are rqbit sessions; the first found is imported once (deleted only if inside CacheDir).
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
	cl        *bt.Client
	pieces    storage.PieceCompletion
	byID      map[int]*entry
	byHash    map[metainfo.Hash]*entry
	nextID    int
	inspected map[metainfo.Hash]*inspection
	// Peers each torrent has known, to try again after an outage took them all.
	known map[metainfo.Hash]map[string]remembered
	// peers: each connection's requests and deliveries, for dropping silent peers and rescuing reads.
	peers *ledger
	// busy: something downloads, so complete torrents keep off the client's connection budget.
	busy  bool
	names *layout
	// secret is what kuro's own client sends and a web page cannot.
	secret string
	closed bool

	// Test hooks: adjust the client config; see each torrent as it is added.
	configure func(*bt.ClientConfig)
	onNew     func(*bt.Torrent)
}

type entry struct {
	id       int
	t        *bt.Torrent
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

	// waits: stream reads in progress, by reader, so the janitor can rescue one that waits too long.
	waits map[*watched]readWait
	// quiet: kept off the network (paused, or complete while another downloads).
	quiet bool
	// For regroup: alone since when, the wait before the next try, and tries so far.
	aloneSince  time.Time
	regroupWait time.Duration
	regroups    int
	// For census: who it was connected to, for the log.
	heads headcount
}

// inspection is a torrent added to read its files, kept connected a while so the add that follows has peers.
type inspection struct {
	t       *bt.Torrent
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
		known:     map[metainfo.Hash]map[string]remembered{},
		peers:     newLedger(),
		names:     newLayout(opts.CacheDir),
	}
	e.secret = torrent.NewSecret()
	e.server = &http.Server{Handler: e.guard(l.Addr().String(), e.routes()), ReadHeaderTimeout: 10 * time.Second}
	go e.server.Serve(l)
	e.client = torrent.NewClient("http://" + l.Addr().String()).WithEngine(e).WithSecret(e.secret)
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
	cl, err := bt.NewClient(e.clientConfig(pieces, port))
	if err != nil {
		// Another client holds the port; a random one works, minus the forwarded port's peers.
		e.log.Warn("peer port unavailable; using a random one", "port", port, "err", err)
		for range 5 {
			if cl, err = bt.NewClient(e.clientConfig(pieces, freePort())); err == nil {
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

// freePort finds a port free for TCP and UDP both; on Windows port 0's UDP twin can sit in a reserved range.
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
func (e *Engine) clientConfig(pieces storage.PieceCompletion, port int) *bt.ClientConfig {
	cfg := bt.NewDefaultClientConfig()
	cfg.DataDir = e.opts.CacheDir
	cfg.DefaultStorage = files{base: e.opts.CacheDir, pieces: pieces, names: e.names}
	cfg.ListenPort = port
	if e.opts.PeerLimit > 0 {
		cfg.EstablishedConnsPerTorrent = e.opts.PeerLimit
	}
	if e.opts.UploadLimit > 0 {
		cfg.UploadRateLimiter = rate.NewLimiter(rate.Limit(e.opts.UploadLimit), 256<<10)
	}
	cfg.NoDefaultPortForwarding = e.opts.DisableUPnP
	// Said either way: whether peers can reach kuro decides how a slow swarm goes.
	cfg.UpnpDiscovered = func(routers int) {
		if routers == 0 {
			e.log.Info("no router answered for port forwarding; peers reach kuro only if the peer port is forwarded", "port", port)
		}
	}
	cfg.UpnpMapped = func(proto string, internal, external int, err error) {
		if err != nil {
			e.log.Info("the router refused to forward the peer port", "proto", proto, "port", internal, "err", err)
			return
		}
		e.log.Info("the router forwards the peer port", "proto", proto, "port", internal, "external", external)
	}
	cfg.Seed = true
	cfg.DisableWebtorrent = true
	cfg.Logger = alog.Default.WithFilterLevel(alog.Warning)
	cfg.Slogger = slog.New(warnOnly{e.log.Handler()})
	e.peers.hook(cfg)
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
		e.writeSession(true)
		cl.Close()
	}
	if pieces != nil {
		pieces.Close()
	}
}

// Var so tests need not wait.
var janitorEvery = 5 * time.Second

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
		var looking []*bt.Torrent
		for hash, in := range e.inspected {
			if now.After(in.expires) {
				delete(e.inspected, hash)
				if _, managed := e.byHash[hash]; !managed {
					in.t.Drop()
					delete(e.known, hash)
					e.names.forget(hash)
				}
				continue
			}
			looking = append(looking, in.t)
		}
		entries := make([]*entry, 0, len(e.byID))
		for _, en := range e.byID {
			entries = append(entries, en)
		}
		e.mu.Unlock()
		// A torrent only being looked at meets its peers before it is added; they count from then.
		for _, t := range looking {
			peers := connected(t)
			e.mu.Lock()
			e.remember(t.InfoHash(), peers, now)
			e.mu.Unlock()
		}
		for _, en := range entries {
			e.shed(en, now)
			e.rescue(en, now)
			e.regroup(en, now)
			e.census(en, now)
		}
		e.focus(entries, len(looking))
	}
}

// shed drops peers holding requests that stopped sending: bt never times a request out, only steals it.
func (e *Engine) shed(en *entry, now time.Time) {
	// Only with another peer to take the requests: a lone seeder may just be pausing.
	if en.t.Info() == nil || en.t.Stats().ActivePeers < 2 {
		return
	}
	for _, pc := range e.peers.silent(en.t.PeerConns(), now) {
		e.log.Info("dropped a peer that stopped sending", "name", en.t.Name(), "peer", addrOf(pc), "quietFor", shedAfter)
		pc.Close()
	}
}

// rescue drops quiet peers holding a long-waiting read's piece, or has all re-plan when none was asked for it.
func (e *Engine) rescue(en *entry, now time.Time) {
	t := en.t
	if t.Info() == nil {
		return
	}
	type stuck struct {
		w    *watched
		wait readWait
	}
	var waiting []stuck
	e.mu.Lock()
	for w, wait := range en.waits {
		if now.Sub(wait.since) >= rescueAfter {
			waiting = append(waiting, stuck{w, wait})
		}
	}
	e.mu.Unlock()
	for _, s := range waiting {
		if t.Piece(s.wait.piece).State().Complete {
			continue
		}
		stale, holders := e.peers.holding(t.PeerConns(), s.wait.piece, rescueAfter, now)
		// Only with another seeder there: the holder may be the one peer with the piece.
		if t.Stats().ConnectedSeeders < 2 {
			stale = nil
		}
		for _, pc := range stale {
			e.log.Info("dropped a peer holding the piece a stream waits on", "name", t.Name(), "piece", s.wait.piece,
				"peer", addrOf(pc), "waited", now.Sub(s.wait.since).Round(time.Second))
			pc.Close()
		}
		if holders > 0 {
			continue
		}
		// Peers plan requests only on their own events; with none coming the piece would never be asked for.
		t.UpdateRequests()
		if s.wait.reported {
			continue
		}
		st := t.Stats()
		e.log.Info("a stream waits on a piece no peer was asked for", "name", t.Name(), "piece", s.wait.piece,
			"waited", now.Sub(s.wait.since).Round(time.Second), "peers", st.ActivePeers, "seeders", st.ConnectedSeeders)
		e.mu.Lock()
		if cur, ok := en.waits[s.w]; ok && cur.since.Equal(s.wait.since) {
			cur.reported = true
			en.waits[s.w] = cur
		}
		e.mu.Unlock()
	}
}

// focus: all torrents dial from one pool, so complete ones stop seeding while anything downloads.
func (e *Engine) focus(entries []*entry, inspecting int) {
	busy := inspecting > 0
	e.mu.Lock()
	for _, en := range entries {
		// Fetching its file list is downloading too, the stretch where peers matter most.
		if !en.paused && (en.t.Info() == nil || wantsData(en)) {
			busy = true
		}
	}
	e.busy = busy
	e.mu.Unlock()
	for _, en := range entries {
		e.quieten(en)
	}
}

// refocus applies focus at once: a new download is most at stake in its first seconds.
func (e *Engine) refocus() {
	e.mu.Lock()
	entries := make([]*entry, 0, len(e.byID))
	for _, en := range e.byID {
		entries = append(entries, en)
	}
	looking := len(e.inspected)
	e.mu.Unlock()
	e.focus(entries, looking)
}

// quieten keeps a torrent off the network while paused, or while complete and another downloads.
func (e *Engine) quieten(en *entry) {
	if en.t.Info() == nil {
		return
	}
	e.mu.Lock()
	quiet := en.paused || (e.busy && !wantsData(en))
	changed := quiet != en.quiet
	en.quiet = quiet
	e.mu.Unlock()
	if !changed {
		return
	}
	if quiet {
		en.t.DisallowDataUpload()
	} else {
		en.t.AllowDataUpload()
	}
}

func addrOf(pc *bt.PeerConn) string {
	if pc.RemoteAddr == nil {
		return ""
	}
	return pc.RemoteAddr.String()
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

// warnOnly passes bt's warnings and errors to kuro's log; below that it logs a line per peer event.
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

func (e *Engine) added(t *bt.Torrent) {
	if e.onNew != nil {
		e.onNew(t)
	}
}
