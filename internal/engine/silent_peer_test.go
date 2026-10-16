package engine

import (
	"bufio"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/time/rate"
	"kuro/internal/bt"
	"kuro/internal/bt/bencode"
	"kuro/internal/bt/metainfo"
	pp "kuro/internal/bt/peer_protocol"
)

// Local repro: a stream read stuck on a chunk a silent peer holds while a healthy peer keeps delivering others.
// SILENTPEER=1; SILENTMIB, SILENTPIECEKIB, SILENTHEALTHY/SLOW/VIDEO (KiB/s), SILENTREQQ, SILENTSECS.
func TestSilentPeer(t *testing.T) {
	if os.Getenv("SILENTPEER") != "1" {
		t.Skip("set SILENTPEER=1")
	}
	k := silentKnobs{
		mib: silentEnv("SILENTMIB", 96), pieceKiB: silentEnv("SILENTPIECEKIB", 1024),
		healthyKiB: silentEnv("SILENTHEALTHY", 384), slowKiB: silentEnv("SILENTSLOW", 32),
		videoKiB: silentEnv("SILENTVIDEO", 128), reqq: silentEnv("SILENTREQQ", 1024),
		observe: time.Duration(silentEnv("SILENTSECS", 60)) * time.Second,
	}
	dir, mi, data := silentTorrent(t, k)
	t.Logf("knobs %+v; a full healthy queue drains in %.0fs", k,
		float64(min(k.reqq, 963)*16)/float64(k.healthyKiB))

	// The healthy peer alone, observed from the moment its queue is deep.
	t.Run("Baseline", func(t *testing.T) {
		r := silentStart(t, k, dir, mi, data)
		r.join(r.fake("healthy", k.healthyKiB<<10, -1))
		r.stream()
		r.awaitDeep()
		r.observe(k.observe)
		r.report()
	})
	// A peer that never sends joins once the healthy peer's queue is deep: as the poorer peer it may steal.
	t.Run("SilentJoinsLate", func(t *testing.T) {
		r := silentStart(t, k, dir, mi, data)
		r.join(r.fake("healthy", k.healthyKiB<<10, -1))
		r.stream()
		r.awaitDeep()
		r.join(r.fake("silent", 0, 0))
		r.observe(k.observe)
		r.report()
	})
	// Same, but the late peer does send, slowly.
	t.Run("SlowJoinsLate", func(t *testing.T) {
		r := silentStart(t, k, dir, mi, data)
		r.join(r.fake("healthy", k.healthyKiB<<10, -1))
		r.stream()
		r.awaitDeep()
		r.join(r.fake("silent", k.slowKiB<<10, -1))
		r.observe(k.observe)
		r.report()
	})
	// The silent peer is alone first, serves 1 MiB, then holds the reader's next chunks; the healthy one joins.
	t.Run("SilentFirst", func(t *testing.T) {
		r := silentStart(t, k, dir, mi, data)
		s := r.join(r.fake("silent", k.healthyKiB<<10, 1<<20))
		r.stream()
		r.awaitSilentHolds(s)
		r.join(r.fake("healthy", k.healthyKiB<<10, -1))
		r.observe(k.observe)
		r.report()
	})
	// Both from the start, the silent one never sending.
	t.Run("Together", func(t *testing.T) {
		r := silentStart(t, k, dir, mi, data)
		r.join(r.fake("healthy", k.healthyKiB<<10, -1))
		r.join(r.fake("silent", 0, 0))
		r.stream()
		r.observe(k.observe)
		r.report()
	})
	// Swarm churn, opt-in (SILENTCHURN=1): a fresh silent peer each time the healthy queue is refilled.
	t.Run("Churn", func(t *testing.T) {
		if os.Getenv("SILENTCHURN") != "1" {
			t.Skip("set SILENTCHURN=1")
		}
		r := silentStart(t, k, dir, mi, data)
		r.join(r.fake("healthy", k.healthyKiB<<10, -1))
		r.stream()
		for i := range 3 {
			r.awaitDeep()
			r.join(r.fake(fmt.Sprintf("silent%d", i+1), 0, 0))
			// Wait for the healthy queue to drain before the next one, so each joins a fresh refill.
			for r.led.outstanding()["healthy"] > 100 {
				time.Sleep(20 * time.Millisecond)
			}
		}
		r.observe(20 * time.Second)
		r.report()
	})
}

type silentKnobs struct {
	mib, pieceKiB, healthyKiB, slowKiB, videoKiB, reqq int
	observe                                            time.Duration
	// fast: a tenth of kuro's drop and rescue timings, so a regression test runs in seconds.
	fast bool
}

func silentEnv(name string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil && v > 0 {
		return v
	}
	return def
}

// silentTorrent is one episode-sized file in a folder, as a pack of one.
func silentTorrent(t *testing.T, k silentKnobs) (string, metainfo.MetaInfo, []byte) {
	t.Helper()
	dir := t.TempDir()
	root := filepath.Join(dir, "Silent Pack")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, k.mib<<20)
	rand.Read(data)
	if err := os.WriteFile(filepath.Join(root, "Show - 01.mkv"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	info := metainfo.Info{PieceLength: int64(k.pieceKiB) << 10}
	if err := info.BuildFromFilePath(root); err != nil {
		t.Fatal(err)
	}
	var mi metainfo.MetaInfo
	var err error
	if mi.InfoBytes, err = bencode.Marshal(info); err != nil {
		t.Fatal(err)
	}
	return dir, mi, data
}

// silentRun is one scenario: kuro's engine, its peers, a paced reader on the stream URL and what was seen.
type silentRun struct {
	t        *testing.T
	k        silentKnobs
	e        *Engine
	en       *entry
	id       int
	led      *silentLedger
	hash     metainfo.Hash
	data     []byte
	pieceLen int64
	size     int64
	drops    atomic.Int32
	// sheds: drops for sending nothing at all, as against holding the piece a read waits on.
	sheds   atomic.Int32
	logDone atomic.Bool
	fakes   []*silentSeed

	start    time.Time
	pos      atomic.Int64
	since    atomic.Int64
	ctx      context.Context
	stop     context.CancelFunc
	finished chan struct{}
	wg       sync.WaitGroup

	mu          sync.Mutex
	blockHolder string
	blocks      []silentBlock
	longest     silentBlock
	starved     silentStarve
}

type silentBlock struct {
	at     int64
	when   time.Duration
	took   time.Duration
	grew   int64
	holder string
}

type silentStarve struct {
	secs   int
	at     int64
	when   time.Duration
	grew   int64
	holder string
}

func silentStart(t *testing.T, k silentKnobs, dir string, mi metainfo.MetaInfo, data []byte) *silentRun {
	t.Helper()
	// kuro's own timings, or a tenth of them for the regression tests; TestMain shortens some for the others.
	j, sh, re, g, c := janitorEvery, shedAfter, rescueAfter, regroupAfter, censusEvery
	janitorEvery, shedAfter, rescueAfter, regroupAfter, censusEvery = 5*time.Second, 15*time.Second, 8*time.Second, 20*time.Second, 30*time.Second
	if k.fast {
		janitorEvery, shedAfter, rescueAfter = 500*time.Millisecond, 1500*time.Millisecond, 800*time.Millisecond
	}
	t.Cleanup(func() { janitorEvery, shedAfter, rescueAfter, regroupAfter, censusEvery = j, sh, re, g, c })

	info, err := mi.UnmarshalInfo()
	if err != nil {
		t.Fatal(err)
	}
	r := &silentRun{t: t, k: k, led: newSilentLedger(), hash: mi.HashInfoBytes(), data: data,
		pieceLen: info.PieceLength, size: info.TotalLength()}
	// Metadata only: closed before the add, so every byte comes from the scenario's own peers.
	meta := seeder(t, dir, mi)
	metaAddr := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: meta.LocalPort()}
	e, err := New(Options{CacheDir: t.TempDir(), SessionDir: t.TempDir()}, slog.New(&silentLog{r: r}))
	if err != nil {
		t.Fatal(err)
	}
	e.configure = func(cfg *bt.ClientConfig) { quietConfig(cfg); r.led.hook(cfg) }
	e.onNew = func(tt *bt.Torrent) { tt.AddPeers([]bt.PeerInfo{{Addr: metaAddr, Trusted: true}}) }
	t.Cleanup(e.Stop)
	t.Cleanup(func() { r.logDone.Store(true) })

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	magnet := magnetOf(t, &mi)
	in, err := e.Client().Inspect(ctx, magnet)
	if err != nil {
		t.Fatal(err)
	}
	meta.Close()
	added, err := e.Client().Add(ctx, magnet, in.Details.Files[0])
	if err != nil {
		t.Fatal(err)
	}
	r.e, r.id = e, added.ID
	if r.en, err = e.entryByID(added.ID); err != nil {
		t.Fatal(err)
	}
	r.led.tor.Store(r.en.t)
	return r
}

func (r *silentRun) elapsed() float64 {
	if r.start.IsZero() {
		return 0
	}
	return time.Since(r.start).Seconds()
}

func (r *silentRun) fake(name string, bps int, serve int64) *silentSeed {
	f := startSilentSeed(r.t, name, r.hash, r.data, r.pieceLen, bps, serve, r.k.reqq)
	r.fakes = append(r.fakes, f)
	return f
}

func (r *silentRun) join(f *silentSeed) *silentSeed {
	r.led.named(f.addr.String(), f.name)
	r.en.t.AddPeers([]bt.PeerInfo{{Addr: f.addr, Trusted: true}})
	r.t.Logf("%4.0fs: %s joins (%s)", r.elapsed(), f.name, f.describe())
	return f
}

// stream opens the stream URL as ffmpeg does and reads it at -readrate 2 with a 60 s initial burst.
func (r *silentRun) stream() {
	r.ctx, r.stop = context.WithCancel(context.Background())
	r.finished = make(chan struct{})
	r.start = time.Now()
	req, _ := http.NewRequestWithContext(r.ctx, http.MethodGet, r.e.Client().StreamURL(r.id, 0), nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		r.t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		r.t.Fatalf("stream: %s", res.Status)
	}
	r.wg.Add(2)
	go func() {
		defer r.wg.Done()
		defer close(r.finished)
		defer res.Body.Close()
		r.read(res.Body)
	}()
	go func() { defer r.wg.Done(); r.monitor() }()
	r.t.Cleanup(r.halt)
}

// blockMin is the shortest wait recorded as a blocked read: a second under the fast timings.
func (r *silentRun) blockMin() time.Duration {
	if r.k.fast {
		return time.Second
	}
	return 5 * time.Second
}

func (r *silentRun) halt() {
	if r.stop != nil {
		r.stop()
		r.wg.Wait()
	}
}

func (r *silentRun) pace() (burst, speed float64) {
	video := float64(r.k.videoKiB << 10)
	return 60 * video, 2 * video
}

func (r *silentRun) read(body io.Reader) {
	burst, pace := r.pace()
	buf := make([]byte, 32<<10)
	var pos int64
	for r.ctx.Err() == nil && pos < r.size {
		if ahead := float64(pos) - (burst + pace*time.Since(r.start).Seconds()); ahead > 0 {
			select {
			case <-r.ctx.Done():
				return
			case <-time.After(time.Duration(ahead / pace * float64(time.Second))):
			}
		}
		began := time.Now()
		r.since.Store(began.UnixNano())
		before := r.led.total()
		n, err := body.Read(buf)
		r.since.Store(0)
		took := time.Since(began)
		r.mu.Lock()
		holder := r.blockHolder
		r.blockHolder = ""
		if took > r.blockMin() && r.ctx.Err() == nil {
			b := silentBlock{at: pos, when: began.Sub(r.start), took: took, grew: r.led.total() - before, holder: holder}
			r.blocks = append(r.blocks, b)
			if b.took > r.longest.took {
				r.longest = b
			}
		}
		r.mu.Unlock()
		pos += int64(n)
		r.pos.Store(pos)
		if err != nil {
			if err != io.EOF && r.ctx.Err() == nil {
				r.t.Logf("read at %d: %v", pos, err)
			}
			return
		}
	}
}

// monitor samples once a second: who holds the chunk a stuck read waits on, and stretches with no progress.
func (r *silentRun) monitor() {
	burst, pace := r.pace()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	var lastPos, runUseful int64
	var run silentStarve
	lastLog := time.Now()
	marks := []int{5, 15, 30, 45, 60, 90, 120, 180, 240}
	end := func() {
		if run.secs == 0 {
			return
		}
		run.grew = r.led.total() - runUseful
		r.mu.Lock()
		if run.secs > r.starved.secs {
			r.starved = run
		}
		r.mu.Unlock()
		if run.secs >= 5 {
			r.t.Logf("  starved stretch over: %ds at %.1f MiB, +%.1f MiB useful meanwhile", run.secs,
				float64(run.at)/(1<<20), float64(run.grew)/(1<<20))
		}
		run = silentStarve{}
	}
	for {
		var now time.Time
		select {
		case <-r.ctx.Done():
			end()
			return
		case now = <-tick.C:
		}
		pos := r.pos.Load()
		behind := burst + pace*now.Sub(r.start).Seconds() - float64(pos)
		// Behind its pace by over a second and getting under a quarter of it: the reader is starved.
		starved := behind > pace && float64(pos-lastPos) < pace/4
		lastPos = pos
		if starved {
			if run.secs == 0 {
				run = silentStarve{at: pos, when: now.Sub(r.start), holder: r.waitingOn(pos)}
				runUseful = r.led.total()
			}
			run.secs++
			if slices.Contains(marks, run.secs) {
				r.t.Logf("  starved %ds at %.1f MiB: waiting on %s; useful +%.1f MiB since; outstanding %v",
					run.secs, float64(pos)/(1<<20), r.waitingOn(pos), float64(r.led.total()-runUseful)/(1<<20), r.led.outstanding())
			}
		} else {
			end()
		}
		if since := r.since.Load(); since != 0 && now.Sub(time.Unix(0, since)) >= 2*time.Second {
			r.mu.Lock()
			first := r.blockHolder == ""
			if first {
				r.blockHolder = r.waitingOn(pos)
			}
			r.mu.Unlock()
			if first && os.Getenv("SILENTTRACE") == "1" {
				c, _, _, _ := r.led.waiting(pos, r.pieceLen, r.size)
				var states []string
				for i := max(0, int(c.piece)-1); i < min(r.en.t.NumPieces(), int(c.piece)+4); i++ {
					st := r.en.t.Piece(i).State()
					states = append(states, fmt.Sprintf("%d:%v/partial=%v/complete=%v", i, st.Priority, st.Partial, st.Complete))
				}
				r.led.mu.Lock()
				at := r.led.batchState["healthy"]
				r.led.mu.Unlock()
				r.t.Logf("  trace at %.1f MiB: %s; runs %s; pieces now %v; %s; history of %d/%d: %v", float64(pos)/(1<<20),
					r.led.batchOf("healthy", c), r.led.runs("healthy"), states, at, c.piece, c.begin/(16<<10), r.led.history(c))
			}
		}
		if now.Sub(lastLog) >= 10*time.Second {
			lastLog = now
			r.t.Logf("%4.0fs: read %.1f MiB, useful %.1f MiB %v, %.0fs behind pace, outstanding %v, drops %d",
				now.Sub(r.start).Seconds(), float64(pos)/(1<<20), float64(r.led.total())/(1<<20), r.led.usefulMiB(),
				max(0, behind/pace), r.led.outstanding(), r.drops.Load())
		}
	}
}

// waitingOn names the first chunk at or after pos not yet received, and who holds its request.
func (r *silentRun) waitingOn(pos int64) string {
	k, h, held, found := r.led.waiting(pos, r.pieceLen, r.size)
	if !found {
		return "nothing missing"
	}
	where := fmt.Sprintf("piece %d chunk %d", k.piece, k.begin/(16<<10))
	if !held {
		return where + " (not requested from anyone)"
	}
	return fmt.Sprintf("%s (held by %s for %.0fs)", where, h.peer, time.Since(h.since).Seconds())
}

// awaitDeep waits for the healthy peer to hold a full request queue, which only drains to be refilled.
func (r *silentRun) awaitDeep() {
	deep := min(r.k.reqq, 963) * 9 / 10
	if r.k.fast {
		// A queue only grows each time it drains, so a while downloading lets it grow as deep as it gets here.
		deep = 8
		time.Sleep(time.Until(r.start.Add(10 * time.Second)))
	}
	deadline := time.Now().Add(4 * time.Minute)
	for r.led.outstanding()["healthy"] < deep {
		if time.Now().After(deadline) {
			r.t.Fatalf("the healthy peer never held %d requests: %v", deep, r.led.outstanding())
		}
		time.Sleep(20 * time.Millisecond)
	}
	r.t.Logf("%4.0fs: healthy holds %d requests, read %.1f MiB", r.elapsed(), r.led.outstanding()["healthy"],
		float64(r.pos.Load())/(1<<20))
}

func (r *silentRun) awaitSilentHolds(s *silentSeed) {
	deadline := time.Now().Add(time.Minute)
	for !(s.quiet() && r.led.outstanding()[s.name] > 0) {
		if time.Now().After(deadline) {
			r.t.Fatalf("the silent peer never went quiet holding requests: %v", r.led.outstanding())
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(time.Second)
	r.t.Logf("%4.0fs: %s went quiet after %d KiB holding %d requests; read %.1f MiB, next %s", r.elapsed(), s.name,
		s.sent.Load()>>10, r.led.outstanding()[s.name], float64(r.pos.Load())/(1<<20), r.waitingOn(r.pos.Load()))
}

func (r *silentRun) observe(d time.Duration) {
	select {
	case <-time.After(d):
	case <-r.finished:
	}
	r.halt()
}

func (r *silentRun) report() {
	r.mu.Lock()
	defer r.mu.Unlock()
	mib := func(n int64) float64 { return float64(n) / (1 << 20) }
	over30 := 0
	for _, b := range r.blocks {
		if b.took > 30*time.Second {
			over30++
		}
		r.t.Logf("  read blocked %.1fs from %.0fs at %.1f MiB, +%.1f MiB useful meanwhile, %s", b.took.Seconds(),
			b.when.Seconds(), mib(b.at), mib(b.grew), b.holder)
	}
	var peers []string
	for _, f := range r.fakes {
		peers = append(peers, fmt.Sprintf("%s sent %.1f MiB for %d requests, %d cancelled, still holds %v", f.name,
			mib(f.sent.Load()), f.asked.Load(), f.rejected.Load(), r.led.heldBy(f.name, 6)))
	}
	r.t.Logf("RESULT %s: longest read %.1fs (at %.1f MiB, from %.0fs, +%.1f MiB useful meanwhile, %s); reads over 30s: %d;"+
		" longest starved stretch %ds (at %.1f MiB, from %.0fs, +%.1f MiB useful, %s); read %.1f MiB of %.0f; drops %d;"+
		" steals %v; %s", r.t.Name(), r.longest.took.Seconds(), mib(r.longest.at), r.longest.when.Seconds(), mib(r.longest.grew),
		r.longest.holder, over30, r.starved.secs, mib(r.starved.at), r.starved.when.Seconds(), mib(r.starved.grew),
		r.starved.holder, mib(r.pos.Load()), mib(r.size), r.drops.Load(), r.led.stealCounts(), strings.Join(peers, "; "))
}

// silentLog passes kuro's info lines to the test log and counts the peers kuro dropped.
type silentLog struct{ r *silentRun }

func (h *silentLog) Enabled(_ context.Context, l slog.Level) bool { return l >= slog.LevelInfo }

func (h *silentLog) Handle(_ context.Context, rec slog.Record) error {
	if strings.Contains(rec.Message, "dropped a peer") {
		h.r.drops.Add(1)
	}
	if strings.Contains(rec.Message, "stopped sending") {
		h.r.sheds.Add(1)
	}
	if h.r.logDone.Load() {
		return nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%4.0fs: engine: %s", h.r.elapsed(), rec.Message)
	rec.Attrs(func(a slog.Attr) bool { fmt.Fprintf(&b, " %s=%v", a.Key, a.Value); return true })
	h.r.t.Log(b.String())
	return nil
}

func (h *silentLog) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *silentLog) WithGroup(string) slog.Handler      { return h }

type silentChunk struct{ piece, begin uint32 }

type silentHolding struct {
	peer  string
	since time.Time
}

// silentLedger follows every request kuro's client sends, through bt's callbacks (called under its lock).
type silentLedger struct {
	mu     sync.Mutex
	names  map[string]string
	holder map[silentChunk]silentHolding
	got    map[silentChunk]bool
	useful map[string]int64
	steals map[string]int
	// Per peer: requests outstanding, and the batch sent since it last had none, in order.
	count   map[string]int
	batch   map[string][]silentChunk
	batchAt map[string]time.Time
	// Piece states near the previous batch's end, read just after a new batch began (SILENTTRACE).
	tor        atomic.Pointer[bt.Torrent]
	lastSent   map[string]silentChunk
	batchState map[string]string
	hist       map[silentChunk][]string
	lastDel    struct {
		c    silentChunk
		peer string
		ok   bool
	}
}

func newSilentLedger() *silentLedger {
	return &silentLedger{names: map[string]string{}, holder: map[silentChunk]silentHolding{},
		got: map[silentChunk]bool{}, useful: map[string]int64{}, steals: map[string]int{},
		count: map[string]int{}, batch: map[string][]silentChunk{}, batchAt: map[string]time.Time{},
		lastSent: map[string]silentChunk{}, batchState: map[string]string{}, hist: map[silentChunk][]string{}}
}

// note keeps a chunk's last few request events. Caller holds l.mu.
func (l *silentLedger) note(c silentChunk, ev string) {
	h := append(l.hist[c], time.Now().Format("15:04:05.000")+" "+ev)
	l.hist[c] = h[max(0, len(h)-4):]
}

func (l *silentLedger) history(c silentChunk) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.hist[c])
}

// snapshot reads piece states around piece p once bt's lock is free; callbacks run under it.
func (l *silentLedger) snapshot(peer string, p uint32) {
	t := l.tor.Load()
	if t == nil || os.Getenv("SILENTTRACE") != "1" {
		return
	}
	go func() {
		var states []string
		for i := max(0, int(p)-1); i < min(t.NumPieces(), int(p)+3); i++ {
			st := t.Piece(i).State()
			states = append(states, fmt.Sprintf("%d:%v/partial=%v/complete=%v/checking=%v", i, st.Priority, st.Partial,
				st.Complete, st.Hashing || st.QueuedForHash))
		}
		l.mu.Lock()
		l.batchState[peer] = fmt.Sprintf("previous batch ended at %d; just after: %v", p, states)
		l.mu.Unlock()
	}()
}

// batchOf describes a peer's latest batch: when it began, its size, piece span, and where c sits in it.
func (l *silentLedger) batchOf(peer string, c silentChunk) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.batch[peer]
	if len(b) == 0 {
		return peer + ": no batch"
	}
	lo, hi := b[0].piece, b[0].piece
	for _, x := range b {
		lo, hi = min(lo, x.piece), max(hi, x.piece)
	}
	var head []string
	for _, x := range b[:min(4, len(b))] {
		head = append(head, fmt.Sprintf("%d/%d", x.piece, x.begin/(16<<10)))
	}
	return fmt.Sprintf("%s batch %.0fs ago: %d requests, pieces %d-%d, first %v, waiting chunk at index %d",
		peer, time.Since(l.batchAt[peer]).Seconds(), len(b), lo, hi, head, slices.Index(b, c))
}

func (l *silentLedger) named(addr, name string) {
	l.mu.Lock()
	l.names[addr] = name
	l.mu.Unlock()
}

func (l *silentLedger) who(p *bt.Peer) string {
	addr := p.RemoteAddr.String()
	if n, ok := l.names[addr]; ok {
		return n
	}
	return addr
}

func (l *silentLedger) hook(cfg *bt.ClientConfig) {
	cb := &cfg.Callbacks
	cb.SentRequest = append(cb.SentRequest, func(ev bt.PeerRequestEvent) {
		l.mu.Lock()
		defer l.mu.Unlock()
		c, who := silentChunk{uint32(ev.Index), uint32(ev.Begin)}, l.who(ev.Peer)
		// A steal is a delete from one peer followed at once by a request of the same chunk from another.
		if l.lastDel.ok && l.lastDel.c == c && l.lastDel.peer != who {
			l.steals[who+"<-"+l.lastDel.peer]++
		}
		l.lastDel.ok = false
		l.note(c, "sent to "+who)
		l.holder[c] = silentHolding{who, time.Now()}
		if l.count[who] == 0 {
			if prev, ok := l.lastSent[who]; ok {
				l.snapshot(who, prev.piece)
			}
			l.batch[who], l.batchAt[who] = nil, time.Now()
		}
		l.count[who]++
		l.batch[who] = append(l.batch[who], c)
		l.lastSent[who] = c
	})
	cb.DeletedRequest = append(cb.DeletedRequest, func(ev bt.PeerRequestEvent) {
		l.mu.Lock()
		defer l.mu.Unlock()
		c, who := silentChunk{uint32(ev.Index), uint32(ev.Begin)}, l.who(ev.Peer)
		l.note(c, "deleted from "+who)
		if h, ok := l.holder[c]; ok && h.peer == who {
			delete(l.holder, c)
			l.count[who]--
		}
		l.lastDel.c, l.lastDel.peer, l.lastDel.ok = c, who, true
	})
	cb.ReceivedUsefulData = append(cb.ReceivedUsefulData, func(ev bt.ReceivedUsefulDataEvent) {
		l.mu.Lock()
		defer l.mu.Unlock()
		c := silentChunk{uint32(ev.Message.Index), uint32(ev.Message.Begin)}
		l.got[c] = true
		l.note(c, "received from "+l.who(ev.Peer))
		l.useful[l.who(ev.Peer)] += int64(len(ev.Message.Piece))
		l.lastDel.ok = false
	})
}

func (l *silentLedger) total() (n int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, v := range l.useful {
		n += v
	}
	return n
}

func (l *silentLedger) usefulMiB() map[string]string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := map[string]string{}
	for k, v := range l.useful {
		out[k] = fmt.Sprintf("%.1f", float64(v)/(1<<20))
	}
	return out
}

func (l *silentLedger) outstanding() map[string]int {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := map[string]int{}
	for _, h := range l.holder {
		out[h.peer]++
	}
	return out
}

// runs is a peer's latest batch in request order as piece runs, e.g. 3/60 8/0-63.
func (l *silentLedger) runs(peer string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var parts []string
	b := l.batch[peer]
	for i := 0; i < len(b); {
		j := i
		for j+1 < len(b) && b[j+1].piece == b[i].piece && b[j+1].begin == b[j].begin+16<<10 {
			j++
		}
		if j == i {
			parts = append(parts, fmt.Sprintf("%d/%d", b[i].piece, b[i].begin/(16<<10)))
		} else {
			parts = append(parts, fmt.Sprintf("%d/%d-%d", b[i].piece, b[i].begin/(16<<10), b[j].begin/(16<<10)))
		}
		i = j + 1
	}
	return strings.Join(parts, " ")
}

// heldBy lists up to n chunks a peer holds, as piece/chunk, and how many in all.
func (l *silentLedger) heldBy(peer string, n int) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var held []silentChunk
	for c, h := range l.holder {
		if h.peer == peer {
			held = append(held, c)
		}
	}
	slices.SortFunc(held, func(a, b silentChunk) int {
		return int(a.piece)*1<<20 + int(a.begin) - int(b.piece)*1<<20 - int(b.begin)
	})
	var parts []string
	for _, c := range held[:min(n, len(held))] {
		parts = append(parts, fmt.Sprintf("%d/%d", c.piece, c.begin/(16<<10)))
	}
	return fmt.Sprintf("%d [%s]", len(held), strings.Join(parts, " "))
}

func (l *silentLedger) stealCounts() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var parts []string
	for k, v := range l.steals {
		parts = append(parts, fmt.Sprintf("%s:%d", k, v))
	}
	sort.Strings(parts)
	return "[" + strings.Join(parts, " ") + "]"
}

func (l *silentLedger) waiting(pos, pieceLen, size int64) (silentChunk, silentHolding, bool, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	const chunk = 16 << 10
	for off := pos - pos%chunk; off < size; off += chunk {
		c := silentChunk{uint32(off / pieceLen), uint32(off % pieceLen)}
		if !l.got[c] {
			h, held := l.holder[c]
			return c, h, held, true
		}
	}
	return silentChunk{}, silentHolding{}, false, false
}

// silentSeed is a minimal BitTorrent seeder: FIFO like libtorrent, at a set rate, silent after serve bytes.
type silentSeed struct {
	name     string
	addr     *net.TCPAddr
	hash     metainfo.Hash
	data     []byte
	pieceLen int64
	bps      int
	lim      *rate.Limiter
	serve    int64
	reqq     int
	ctx      context.Context

	sent, asked, rejected atomic.Int64
}

func startSilentSeed(t *testing.T, name string, hash metainfo.Hash, data []byte, pieceLen int64, bps int, serve int64, reqq int) *silentSeed {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &silentSeed{name: name, addr: ln.Addr().(*net.TCPAddr), hash: hash, data: data, pieceLen: pieceLen,
		bps: bps, serve: serve, reqq: reqq, ctx: ctx, lim: rate.NewLimiter(rate.Inf, 0)}
	if bps > 0 {
		s.lim = rate.NewLimiter(rate.Limit(bps), 16<<10)
	}
	var wg sync.WaitGroup
	t.Cleanup(func() { cancel(); ln.Close(); wg.Wait() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Go(func() { s.conn(c) })
		}
	}()
	return s
}

func (s *silentSeed) describe() string {
	switch {
	case s.serve == 0:
		return "never sends a chunk"
	case s.serve > 0:
		return fmt.Sprintf("%d KiB/s for %d KiB, then never again", s.bps>>10, s.serve>>10)
	case s.bps > 0:
		return fmt.Sprintf("%d KiB/s", s.bps>>10)
	}
	return "unlimited"
}

func (s *silentSeed) quiet() bool { return s.serve >= 0 && s.sent.Load() >= s.serve }

func (s *silentSeed) conn(c net.Conn) {
	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()
	go func() { <-ctx.Done(); c.Close() }()
	hs := make([]byte, 68)
	if _, err := io.ReadFull(c, hs); err != nil || string(hs[:20]) != pp.Protocol || string(hs[28:48]) != string(s.hash[:]) {
		return // an obfuscated attempt: bt dials again in plain text
	}
	var wmu sync.Mutex
	send := func(b []byte) {
		wmu.Lock()
		c.Write(b)
		wmu.Unlock()
	}
	msg := func(m pp.Message) { send(m.MustMarshalBinary()) }
	var id [20]byte
	copy(id[:], "-SP0001-")
	rand.Read(id[8:])
	ext := pp.NewPeerExtensionBytes(pp.ExtensionBitFast, pp.ExtensionBitLtep)
	send(slices.Concat([]byte(pp.Protocol), ext[:], s.hash[:], id[:]))
	hello, _ := bencode.Marshal(pp.ExtendedHandshakeMessage{M: map[pp.ExtensionName]pp.ExtensionNumber{}, V: "silent-peer", Reqq: s.reqq})
	msg(pp.Message{Type: pp.Extended, ExtendedID: pp.HandshakeExtendedID, ExtendedPayload: hello})
	if s.name != "healthy" && os.Getenv("SILENTBITFIELD") == "1" {
		// All but the last piece: bt counts each piece, refreshing its cached order; a full one is HaveAll.
		have := make([]bool, (int64(len(s.data))+s.pieceLen-1)/s.pieceLen)
		for i := range len(have) - 1 {
			have[i] = true
		}
		msg(pp.Message{Type: pp.Bitfield, Bitfield: have})
	} else {
		msg(pp.Message{Type: pp.HaveAll})
	}
	msg(pp.Message{Type: pp.Unchoke})

	var mu sync.Mutex
	wake := sync.NewCond(&mu)
	var queue []pp.RequestSpec
	done := false
	go func() {
		<-ctx.Done()
		mu.Lock()
		done = true
		wake.Broadcast()
		mu.Unlock()
	}()
	go func() {
		tick := time.NewTicker(20 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				send([]byte{0, 0, 0, 0})
			}
		}
	}()
	// Requests are served in the order they came, at the limiter's pace, until the peer goes quiet.
	go func() {
		for {
			mu.Lock()
			for !done && (len(queue) == 0 || s.quiet()) {
				wake.Wait()
			}
			if done {
				mu.Unlock()
				return
			}
			r := queue[0]
			mu.Unlock()
			if s.lim.WaitN(ctx, int(r.Length)) != nil {
				return
			}
			mu.Lock()
			if len(queue) == 0 || queue[0] != r {
				mu.Unlock()
				continue
			}
			queue = queue[1:]
			mu.Unlock()
			off := int64(r.Index)*s.pieceLen + int64(r.Begin)
			msg(pp.Message{Type: pp.Piece, Index: r.Index, Begin: r.Begin, Piece: s.data[off : off+int64(r.Length)]})
			s.sent.Add(int64(r.Length))
		}
	}()
	dec := pp.Decoder{R: bufio.NewReader(c), MaxLength: 1 << 20}
	for {
		var m pp.Message
		if err := dec.Decode(&m); err != nil {
			return
		}
		if m.Keepalive {
			continue
		}
		switch m.Type {
		case pp.Request:
			s.asked.Add(1)
			mu.Lock()
			queue = append(queue, m.RequestSpec())
			wake.Broadcast()
			mu.Unlock()
		case pp.Cancel:
			// Fast extension: a cancelled request still queued is answered with a reject, as libtorrent does.
			r := m.RequestSpec()
			mu.Lock()
			i := slices.Index(queue, r)
			if i >= 0 {
				queue = slices.Delete(queue, i, i+1)
			}
			mu.Unlock()
			if i >= 0 {
				s.rejected.Add(1)
				msg(pp.Message{Type: pp.Reject, Index: r.Index, Begin: r.Begin, Length: r.Length})
			}
		}
	}
}
