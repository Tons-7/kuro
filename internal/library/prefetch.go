package library

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"kuro/internal/score"
	"kuro/internal/store"
	"kuro/internal/torrent"
)

// Prefetcher pulls the next episode while the current one plays, so pressing
// next starts immediately instead of paying the swarm cost again.
const (
	// A failed resolve usually means "not subbed yet", unchanged for minutes.
	// Without a pause, every series-page visit re-searches every indexer.
	prepareRetry = 10 * time.Minute

	// How long a starting episode waits for a resolve already under way: enough to
	// cover one, not so long a stuck prepare becomes the delay it removes.
	prepareWait = 45 * time.Second
)

type Prefetcher struct {
	store   *store.Store
	finder  *Finder
	torrent *torrent.Client
	log     *slog.Logger

	mu sync.Mutex
	// Closed when the work for that key finishes, so playback can wait on a
	// resolve already running rather than starting a second one beside it.
	running map[string]chan struct{}
	// When a resolve last failed, so it is not retried on every page view.
	attempted map[string]time.Time
	// Releases resolved ahead of play, in memory only: the engine is untouched until then.
	prepared map[string]preparedRelease
	// Torrents this process started or resumed, which the startup quiet leaves be.
	claimed map[string]bool
	// Episodes being fetched right now, so a second fetch of one waits and play takes the torrent over.
	fetching map[string]*inFlightFetch
	// room asks the cache to fit that many more bytes for a show's next episode. Nil: a full cache refuses.
	room func(ctx context.Context, need int64, animeID int) bool
}

// inFlightFetch is one episode's fetch under way: its torrent once added, and when the fetch ends.
type inFlightFetch struct {
	added chan struct{}
	done  chan struct{}
	once  sync.Once
	rec   store.TorrentRecord // under Prefetcher.mu; an empty hash until added, or after a discard
	// Under Prefetcher.mu: the hash this fetch claimed from adoption, and whether play took its torrent over.
	hash  string
	taken bool
}

// errOvertaken: another path is fetching this episode, or recorded a release for it meanwhile.
var errOvertaken = errors.New("episode fetched by another path")

func fetchKey(animeID, episode int) string { return fmt.Sprintf("fetch:%d/%d", animeID, episode) }

// beginFetch claims an episode for one fetch at a time; false when another has it.
func (p *Prefetcher) beginFetch(animeID, episode int) (*inFlightFetch, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fetching == nil {
		p.fetching = map[string]*inFlightFetch{}
	}
	key := fetchKey(animeID, episode)
	if _, busy := p.fetching[key]; busy {
		return nil, false
	}
	f := &inFlightFetch{added: make(chan struct{}), done: make(chan struct{})}
	p.fetching[key] = f
	return f, true
}

func (p *Prefetcher) endFetch(animeID, episode int, f *inFlightFetch) {
	p.mu.Lock()
	delete(p.fetching, fetchKey(animeID, episode))
	// Adoption resumes: a torrent left unrecorded is still a download someone should see.
	delete(p.claimed, f.hash)
	p.mu.Unlock()
	f.once.Do(func() { close(f.added) })
	close(f.done)
}

// setTorrent publishes the torrent a fetch has added, or forgets one it discarded (an empty record).
func (p *Prefetcher) setTorrent(f *inFlightFetch, rec store.TorrentRecord) {
	p.mu.Lock()
	f.rec = rec
	p.mu.Unlock()
	if rec.InfoHash != "" {
		f.once.Do(func() { close(f.added) })
	}
}

// Fetching is the torrent a fetch is still warming; play takes it over, and the fetch leaves it alone.
func (p *Prefetcher) Fetching(animeID, episode int) (store.TorrentRecord, bool) {
	if p == nil {
		return store.TorrentRecord{}, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	f := p.fetching[fetchKey(animeID, episode)]
	if f == nil || f.rec.InfoHash == "" {
		return store.TorrentRecord{}, false
	}
	f.taken = true
	return f.rec, true
}

func (p *Prefetcher) takenOver(f *inFlightFetch) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return f.taken
}

// inFlight is the fetch of this episode under way, nil when there is none.
func (p *Prefetcher) inFlight(animeID, episode int) *inFlightFetch {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.fetching[fetchKey(animeID, episode)]
}

// WithRoom lets a prefetch into a full cache evict older episodes instead of being refused.
func (p *Prefetcher) WithRoom(room func(ctx context.Context, need int64, animeID int) bool) *Prefetcher {
	p.room = room
	return p
}

func (p *Prefetcher) claim(f *inFlightFetch, hash string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.claimed == nil {
		p.claimed = map[string]bool{}
	}
	f.hash = strings.ToLower(hash)
	p.claimed[f.hash] = true
}

// claimWhile claims a hash until the returned func runs.
func (p *Prefetcher) claimWhile(hash string) func() {
	hash = strings.ToLower(hash)
	p.mu.Lock()
	if p.claimed == nil {
		p.claimed = map[string]bool{}
	}
	p.claimed[hash] = true
	p.mu.Unlock()
	return func() {
		p.mu.Lock()
		delete(p.claimed, hash)
		p.mu.Unlock()
	}
}

func (p *Prefetcher) Claimed(hash string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.claimed[strings.ToLower(hash)]
}

// Resolved as an episode starts, taken when it ends, so it must outlast one.
// Play still warms it, so a stale pick costs one failed candidate.
const preparedTTL = 4 * time.Hour

// preparedFresh is how long a resolved release is not worth resolving again.
var preparedFresh = 30 * time.Minute

type preparedRelease struct {
	result score.Result
	at     time.Time
}

func NewPrefetcher(s *store.Store, f *Finder, tc *torrent.Client, log *slog.Logger) *Prefetcher {
	return &Prefetcher{
		store: s, finder: f, torrent: tc, log: log,
		running:   map[string]chan struct{}{},
		attempted: map[string]time.Time{},
		prepared:  map[string]preparedRelease{},
	}
}

// Resolving is just an indexer search — no download, nothing added to the
// engine — so it has its own switch, on by default, not the download one.
func (p *Prefetcher) prepareWanted() bool {
	global, err := p.store.Prefs(context.Background(), 0)
	return err == nil && global.Bool("playback.prepare_next")
}

func prepareKey(animeID, episode int) string {
	return fmt.Sprintf("prepare:%d/%d", animeID, episode)
}

// ahead is how many episodes to download past the one playing: 0, 1 or 2.
func (p *Prefetcher) ahead() int {
	global, err := p.store.Prefs(context.Background(), 0)
	if err != nil || !global.Bool("cache.prefetch_next") {
		return 0
	}
	return min(max(global.Int("cache.prefetch_count"), 1), 2)
}

// Opt-in: it shares the line with the episode playing. A failure is logged, never shown.
// The second episode waits for the first to land rather than halve the line.
func (p *Prefetcher) Next(animeID, episode, season int, prefs score.Preferences) {
	if p == nil || p.torrent == nil || animeID == 0 || episode <= 0 {
		return
	}
	count := p.ahead()
	if count == 0 {
		return
	}

	key := fmt.Sprintf("%d/%d", animeID, episode)
	p.mu.Lock()
	if _, busy := p.running[key]; busy {
		p.mu.Unlock()
		return
	}
	done := make(chan struct{})
	p.running[key] = done
	p.mu.Unlock()

	go func() {
		defer func() {
			p.mu.Lock()
			delete(p.running, key)
			p.mu.Unlock()
			close(done)
		}()

		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(count)*30*time.Minute)
		defer cancel()

		at := episode
		for i := range count {
			next, err := p.store.NextEpisode(ctx, animeID, at)
			if err != nil || next == 0 {
				return
			}
			at = next
			id, started, err := p.fetch(ctx, animeID, next, season, prefs, false)
			if err != nil {
				// Said aloud: "it stopped downloading the next one" has to be answerable from the log.
				if !errors.Is(err, errOvertaken) {
					p.log.Info("next episode not downloaded ahead", "anime", animeID, "episode", next, "err", err)
				}
				return
			}
			if started && i < count-1 {
				if err := p.torrent.Await(ctx, id, prefetchStall); err != nil {
					p.log.Debug("prefetch stopped waiting", "anime", animeID, "episode", next, "err", err)
					return
				}
			}
		}
	}()
}

// A prefetch idle this long is no longer waited on.
const prefetchStall = 3 * time.Minute

// Prepare resolves the next episode now, so pressing Next later is immediate.
func (p *Prefetcher) Prepare(animeID, episode, season int, prefs score.Preferences) {
	if p == nil {
		return
	}
	next, err := p.store.NextEpisode(context.Background(), animeID, episode)
	if err != nil || next == 0 {
		return
	}
	p.PrepareTarget(animeID, next, season, prefs)
}

// PrepareTarget does the same for one named episode: whatever the series page
// says "Continue" would play, so pressing it does not wait on a search.
func (p *Prefetcher) PrepareTarget(animeID, episode, season int, prefs score.Preferences) {
	if p == nil || p.torrent == nil || animeID == 0 || episode <= 0 {
		return
	}
	if !p.prepareWanted() {
		return
	}
	p.resolveAsync(animeID, episode, season, prefs)
}

// Ahead resolves a queued episode while the one before it downloads, so it starts without a search.
// Not behind the playback switch: this is the queue's own work, and it downloads nothing.
func (p *Prefetcher) Ahead(animeID, episode, season int, prefs score.Preferences) {
	if p == nil || p.torrent == nil || animeID == 0 || episode <= 0 {
		return
	}
	p.resolveAsync(animeID, episode, season, prefs)
}

func (p *Prefetcher) resolveAsync(animeID, episode, season int, prefs score.Preferences) {
	key := prepareKey(animeID, episode)

	p.mu.Lock()
	if _, busy := p.running[key]; busy {
		p.mu.Unlock()
		return
	}
	if at, tried := p.attempted[key]; tried && time.Since(at) < prepareRetry {
		p.mu.Unlock()
		return
	}
	// Resolved a moment ago: every visit to the series page would otherwise search every indexer again.
	if got, ok := p.prepared[key]; ok && time.Since(got.at) < preparedFresh {
		p.mu.Unlock()
		return
	}
	done := make(chan struct{})
	p.running[key] = done
	p.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		err := p.prepare(ctx, animeID, episode, season, prefs)

		p.mu.Lock()
		delete(p.running, key)
		if err != nil {
			// Expired marks are dropped, or a long session keeps one per failed episode.
			for k, at := range p.attempted {
				if time.Since(at) >= prepareRetry {
					delete(p.attempted, k)
				}
			}
			p.attempted[key] = time.Now()
		} else {
			delete(p.attempted, key)
		}
		p.mu.Unlock()
		close(done)

		if err != nil {
			p.log.Debug("prepare skipped", "anime", animeID, "episode", episode, "err", err)
		}
	}()
}

// AwaitPrepare waits for a running resolve of the episode, and for a fetch of it to add its torrent.
func (p *Prefetcher) AwaitPrepare(ctx context.Context, animeID, episode int) {
	if p == nil {
		return
	}
	deadline := time.After(prepareWait)
	p.awaitResolve(ctx, animeID, episode, deadline)

	p.mu.Lock()
	f := p.fetching[fetchKey(animeID, episode)]
	p.mu.Unlock()
	if f == nil {
		return
	}
	p.log.Info("waiting for the download already started for it", "anime", animeID, "episode", episode)
	select {
	case <-f.added:
	case <-ctx.Done():
	case <-deadline:
	}
}

// awaitResolve is the resolve half: a fetch waits on this itself, not on its own torrent.
func (p *Prefetcher) awaitResolve(ctx context.Context, animeID, episode int, deadline <-chan time.Time) {
	p.mu.Lock()
	done := p.running[prepareKey(animeID, episode)]
	p.mu.Unlock()
	if done == nil {
		return
	}

	p.log.Info("waiting for the release already being made ready",
		"anime", animeID, "episode", episode)
	select {
	case <-done:
	case <-ctx.Done():
	case <-deadline:
	}
}

// held is the release already recorded for an episode, unless it names another
// show: one stored before identity was enforced must not stand in for it.
func (p *Prefetcher) held(ctx context.Context, animeID, episode int) (store.TorrentRecord, bool) {
	rec, ok, err := p.store.TorrentForEpisode(ctx, animeID, epKey(episode))
	if err != nil || !ok || (!rec.Manual && !heldNamesShow(ctx, p.store, animeID, rec.Name)) {
		return store.TorrentRecord{}, false
	}
	return rec, true
}

func (p *Prefetcher) prepare(ctx context.Context, animeID, episode, season int, prefs score.Preferences) error {
	// Already held, or being fetched this moment, so there is nothing to resolve.
	if _, ok := p.held(ctx, animeID, episode); ok || p.inFlight(animeID, episode) != nil {
		return nil
	}

	// A library file plays without a torrent, so there's nothing to search for.
	if _, ok := localCopy(ctx, p.store, p.log, animeID, episode); ok {
		return nil
	}

	total, _ := p.store.EpisodeCount(ctx, animeID)
	if total > 0 && episode > total {
		return nil
	}

	found, err := p.finder.Find(ctx, Request{
		AnimeID: animeID, Episode: episode, Season: season, Prefs: prefs,
	})
	if err != nil {
		return err
	}
	if found.Best == nil {
		return fmt.Errorf("no release found")
	}

	// Remembered in memory only. Play adds it to the engine when it actually
	// starts, so browsing a series downloads nothing and Downloads stays clean.
	p.storePrepared(animeID, episode, *found.Best)
	p.log.Info("next episode resolved", "anime", animeID, "episode", episode, "group", found.Best.Release.Group)
	return nil
}

// storePrepared caches a resolved release and drops any that have gone stale.
func (p *Prefetcher) storePrepared(animeID, episode int, r score.Result) {
	now := time.Now()
	p.mu.Lock()
	defer p.mu.Unlock()
	for k, v := range p.prepared {
		if now.Sub(v.at) > preparedTTL {
			delete(p.prepared, k)
		}
	}
	p.prepared[prepareKey(animeID, episode)] = preparedRelease{result: r, at: now}
}

// takePreparedUnless leaves the resolved release where it is when a release was picked by hand.
func (p *Prefetcher) takePreparedUnless(animeID, episode int, picked bool) (score.Result, bool) {
	if picked {
		return score.Result{}, false
	}
	return p.TakePrepared(animeID, episode)
}

// TakePrepared hands play a release resolved ahead, once; a stale one counts as absent.
func (p *Prefetcher) TakePrepared(animeID, episode int) (score.Result, bool) {
	if p == nil {
		return score.Result{}, false
	}
	key := prepareKey(animeID, episode)
	p.mu.Lock()
	defer p.mu.Unlock()
	got, ok := p.prepared[key]
	if !ok {
		return score.Result{}, false
	}
	delete(p.prepared, key)
	if time.Since(got.at) > preparedTTL {
		return score.Result{}, false
	}
	return got.result, true
}

// Download fetches the episode and waits for it to land, or every queued episode would be added within
// seconds and compete for the line. What lands is kept: asked for, so outside the cache budget.
func (p *Prefetcher) Download(ctx context.Context, animeID, episode, season int, prefs score.Preferences, stall time.Duration) error {
	return p.DownloadRelease(ctx, animeID, episode, season, prefs, stall, "")
}

// DownloadRelease is Download for a release chosen by hand (its info hash): that one is fetched, and a
// copy already held under another release is replaced by it.
func (p *Prefetcher) DownloadRelease(ctx context.Context, animeID, episode, season int, prefs score.Preferences, stall time.Duration, pick string) error {
	if p == nil || p.torrent == nil {
		return fmt.Errorf("torrent engine unavailable")
	}
	for {
		err := p.download(ctx, animeID, episode, season, prefs, stall, pick)
		if !errors.Is(err, errOvertaken) {
			return err
		}
		// Another path is fetching this episode or recorded a release for it: that copy is kept, once on record.
		if f := p.inFlight(animeID, episode); f != nil {
			select {
			case <-f.done:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

func (p *Prefetcher) download(ctx context.Context, animeID, episode, season int, prefs score.Preferences, stall time.Duration, pick string) error {
	// A copy already held — watched earlier, or cancelled or stalled half way — is promoted and resumed
	// rather than fetched a second time, unless another release was chosen over it.
	if rec, ok := p.held(ctx, animeID, episode); ok && (pick == "" || strings.EqualFold(rec.InfoHash, pick)) {
		if _, err := p.store.KeepDownload(ctx, rec.InfoHash, true); err != nil {
			return err
		}
		id, running, err := p.resume(ctx, rec)
		switch {
		case errors.Is(err, errNotInEngine):
			// Recorded, but the engine lost it: fetched again below.
		case err != nil:
			return err
		case !running:
			return nil
		default:
			// Claimed while it runs, so a startup quieting of unfinished torrents leaves it be.
			done := p.claimWhile(rec.InfoHash)
			defer done()
			return p.awaitOrStop(ctx, id, stall)
		}
	}

	id, started, err := p.fetchRelease(ctx, animeID, episode, season, prefs, true, pick)
	if err != nil || !started {
		return err
	}
	return p.awaitOrStop(ctx, id, stall)
}

// awaitOrStop waits for the download, stopping it if the wait ends any other way
// so it does not compete with the next episode. Bytes stay on disk.
func (p *Prefetcher) awaitOrStop(ctx context.Context, id int, stall time.Duration) error {
	err := p.torrent.Await(ctx, id, stall)
	if err != nil && !errors.Is(err, torrent.ErrPaused) {
		p.stop(context.WithoutCancel(ctx), id, "did not finish", err)
	}
	return err
}

// stop pauses a download the fetch gives up on, unless it is playing: a paused torrent freezes its stream.
func (p *Prefetcher) stop(ctx context.Context, id int, what string, reason error) {
	if p.playing(ctx, id) {
		p.log.Info("leaving a download that "+what+" running: it is playing", "torrent", id, "reason", reason)
		return
	}
	p.log.Info("stopping a download that "+what, "torrent", id, "reason", reason)
	if err := p.torrent.Pause(ctx, id); err != nil {
		p.log.Warn("stop abandoned download", "torrent", id, "err", err)
	}
}

// playing reports a torrent whose episode is pinned: a stream has it open.
func (p *Prefetcher) playing(ctx context.Context, id int) bool {
	d, err := p.torrent.Details(ctx, id)
	if err != nil {
		return false
	}
	entries, err := p.store.CacheEntries(ctx)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.Pinned && strings.EqualFold(e.InfoHash, d.InfoHash) {
			return true
		}
	}
	return false
}

// localCopy is the episode's library file if still on disk; a row whose file went is marked missing now.
func localCopy(ctx context.Context, st *store.Store, log *slog.Logger, animeID, episode int) (store.LocalFile, bool) {
	for {
		f, err := st.LocalEpisode(ctx, animeID, episode)
		if err != nil || f.ID == 0 {
			return store.LocalFile{}, false
		}
		if _, err := os.Stat(f.Path); err == nil {
			return f, true
		}
		log.Warn("library file gone; marking it missing", "path", f.Path)
		if err := st.MarkLocalMissing(ctx, f.ID); err != nil {
			return store.LocalFile{}, false
		}
	}
}

var errNotInEngine = errors.New("not held by the engine")

// resume restarts a held, unfinished episode, by hash: ids change across restarts.
func (p *Prefetcher) resume(ctx context.Context, rec store.TorrentRecord) (id int, running bool, err error) {
	live, err := p.torrent.Live(ctx)
	if err != nil {
		return 0, false, err
	}
	id, ok := live[strings.ToLower(rec.InfoHash)]
	if !ok {
		return 0, false, errNotInEngine
	}
	stats, err := p.torrent.Stats(ctx, id)
	if err != nil {
		return 0, false, err
	}
	if stats.Finished {
		return id, false, nil
	}

	if err := p.torrent.Start(ctx, id); err != nil {
		p.log.Debug("resume part-downloaded episode", "torrent", id, "err", err)
	}
	p.log.Info("resuming part-downloaded episode", "anime", rec.AnimeID, "episode", rec.EpKey)
	return id, true, nil
}

// inEngine: recorded and still held. Unanswered counts as held, not a refetch.
func (p *Prefetcher) inEngine(ctx context.Context, animeID, episode int) bool {
	rec, ok := p.held(ctx, animeID, episode)
	if !ok {
		return false
	}
	live, err := p.torrent.Live(ctx)
	if err != nil {
		return true
	}
	_, ok = live[strings.ToLower(rec.InfoHash)]
	return ok
}

// Dead swarms cost the inspect timeout each; three is enough to get past them.
const fetchAttempts = 3

// How long a queued download may take for its first bytes (a slow swarm's first piece); shrunk in tests.
var prefetchWarmCap = 5 * time.Minute

// ErrSeedersSilent: peers answered the handshake and then sent nothing in all of prefetchWarmCap.
var ErrSeedersSilent = errors.New("the seeders never answered")

// fetch reports the torrent id it started and whether it started anything.
// keep puts the episode in the kept tier, which the cache budget does not apply to.
func (p *Prefetcher) fetch(ctx context.Context, animeID, episode, season int, prefs score.Preferences, keep bool) (int, bool, error) {
	return p.fetchRelease(ctx, animeID, episode, season, prefs, keep, "")
}

// fetchRelease is fetch for a release chosen by hand (pick, an info hash): no search ranking, no
// resolve ahead, and a copy held under another release is the one being replaced, not a reason to stop.
func (p *Prefetcher) fetchRelease(ctx context.Context, animeID, episode, season int, prefs score.Preferences, keep bool, pick string) (int, bool, error) {
	// One fetch per episode: a second one beside it would add a second torrent for the same file.
	flight, ok := p.beginFetch(animeID, episode)
	if !ok {
		return 0, false, errOvertaken
	}
	defer p.endFetch(animeID, episode, flight)

	if pick == "" && p.inEngine(ctx, animeID, episode) {
		return 0, false, nil
	}
	prior, _ := p.held(ctx, animeID, episode)

	// Already in the library: downloading a copy of a file on disk is waste.
	if _, ok := localCopy(ctx, p.store, p.log, animeID, episode); ok {
		return 0, false, nil
	}

	// A prefetch never pushes the cache over budget: what plays now beats what might play next.
	var usage store.CacheUsage
	if !keep {
		var err error
		if usage, err = p.store.CacheUsage(ctx); err != nil {
			return 0, false, err
		}
		// With room to ask for, the release's size decides below.
		if p.room == nil && usage.Budget > 0 && usage.Bytes >= usage.Budget {
			return 0, false, fmt.Errorf("cache at budget")
		}
	}

	total, _ := p.store.EpisodeCount(ctx, animeID)
	if total > 0 && episode > total {
		return 0, false, nil
	}

	// Walk a ranked list: the first pick's swarm may be dead.
	var best score.Result
	var inspected, added *torrent.Torrent
	var file torrent.File
	var index int
	lastErr := fmt.Errorf("no release found")
	// A release resolved ahead that then would not start; the search must not hand it back.
	var stale string
	addFirst := func(results []score.Result) error {
		tried := 0
		for _, cand := range results {
			if (!cand.AutoPick && pick == "") || tried >= fetchAttempts || cand.Torrent.InfoHash == stale {
				continue
			}
			if !keep && usage.Budget > 0 && usage.Bytes+cand.EpisodeBytes() > usage.Budget {
				if p.room == nil || !p.room(ctx, cand.EpisodeBytes(), animeID) {
					return fmt.Errorf("would exceed cache budget")
				}
				// Room was made; later candidates are measured against what is left.
				if after, err := p.store.CacheUsage(ctx); err == nil {
					usage = after
				}
			}
			tried++

			// A chosen release has no next candidate to give the time to, so its file list is waited for.
			inspect := p.torrent.Inspect
			if pick != "" {
				inspect = p.torrent.InspectPicked
			}
			ins, err := inspect(ctx, cand.Torrent.Magnet())
			if err != nil {
				lastErr = err
				continue
			}
			f, i, ok := pickFile(ins.Details.Files, cand)
			if !ok {
				lastErr = fmt.Errorf("episode not in torrent")
				continue
			}
			a, err := p.torrent.Add(ctx, cand.Torrent.Magnet(), f)
			if err != nil {
				lastErr = err
				continue
			}
			best, inspected, file, index, added = cand, ins, f, i, a
			return nil
		}
		return nil
	}

	// What this fetch has added; play takes it over from here if it arrives before the record.
	torrentRecord := func() store.TorrentRecord {
		return store.TorrentRecord{
			InfoHash:  best.Torrent.InfoHash,
			EngineID:  added.ID,
			Name:      inspected.Details.Name,
			TotalSize: file.Length,
			AnimeID:   animeID,
			EpKey:     epKey(episode),
			FileIndex: index,
			FilePath:  file.Name,
			// Chosen by hand: the identity check does not get to replace it.
			Manual: pick != "",
		}
	}

	// record makes the torrent this episode's; never pinned, play may have taken it over meanwhile. A
	// different release recorded meanwhile is the viewer's: superseding it would delete it mid-play.
	record := func(ctx context.Context) error {
		if rec, ok := p.held(ctx, animeID, episode); ok && !strings.EqualFold(rec.InfoHash, best.Torrent.InfoHash) &&
			!strings.EqualFold(rec.InfoHash, prior.InfoHash) {
			return errOvertaken
		}
		if err := p.store.RecordTorrent(ctx, torrentRecord()); err != nil {
			return err
		}
		if keep {
			if _, err := p.store.KeepDownload(ctx, best.Torrent.InfoHash, true); err != nil {
				return err
			}
		}
		return nil
	}

	// keepDead records a torrent whose swarm gave nothing too, so the later try resumes it and the
	// list shows it under its episode; false drops it, for a release the search is about to replace.
	start := func(keepDead bool) error {
		p.claim(flight, best.Torrent.InfoHash)
		p.setTorrent(flight, torrentRecord())
		err := p.torrent.WaitLive(ctx, added.ID, 2*time.Minute)
		if err == nil {
			// The stream client waits as long as its context.
			warm, cancel := context.WithTimeout(ctx, prefetchWarmCap)
			err = p.torrent.Prewarm(warm, added.ID, index, 2<<20)
			cancel()
			if err != nil && ctx.Err() == nil && errors.Is(err, context.DeadlineExceeded) {
				err = fmt.Errorf("%w: %v", ErrSeedersSilent, err)
			}
		}
		// Play took the torrent over meanwhile: it is play's to warm, pause or drop, not this fetch's.
		if err != nil && p.takenOver(flight) {
			return errOvertaken
		}
		if err != nil && (ctx.Err() != nil || keepDead) {
			// Interrupted, or dead for now: recorded, kept if asked for, so the retry resumes this very
			// torrent instead of searching again while the sweep lists it under no show.
			switch rerr := record(context.WithoutCancel(ctx)); {
			case errors.Is(rerr, errOvertaken):
				p.setTorrent(flight, store.TorrentRecord{})
				discardUnclaimed(context.WithoutCancel(ctx), p.store, p.torrent, p.log, added.ID, best.Torrent.InfoHash)
				return err
			case rerr != nil:
				p.log.Warn("record interrupted download", "err", rerr)
			}
		}
		if err != nil {
			// Cancelled or dead half way: stopped, not left fetching unrecorded.
			p.stop(context.WithoutCancel(ctx), added.ID, "did not start", err)
		}
		return err
	}

	// Resolved while the episode before it downloaded: tried first, and the search is the fallback.
	if pick == "" {
		p.awaitResolve(ctx, animeID, episode, time.After(prepareWait))
	}
	if rel, ok := p.takePreparedUnless(animeID, episode, pick != ""); ok {
		if err := addFirst([]score.Result{rel}); err != nil {
			// Refused for space, not for being bad: play still wants it when the episode starts.
			p.storePrepared(animeID, episode, rel)
			return 0, false, err
		}
		if added != nil {
			err := start(false)
			if err != nil && (ctx.Err() != nil || errors.Is(err, errOvertaken)) {
				return 0, false, err
			}
			if err != nil {
				p.log.Info("release resolved ahead did not start, searching", "anime", animeID, "episode", episode)
				// Replaced, not retried: left paused it would sit in the downloads list under no show.
				p.setTorrent(flight, store.TorrentRecord{})
				discardUnclaimed(context.WithoutCancel(ctx), p.store, p.torrent, p.log, added.ID, rel.Torrent.InfoHash)
				stale, added, lastErr = rel.Torrent.InfoHash, nil, err
			}
		}
	}
	if added == nil {
		found, err := p.finder.Find(ctx, Request{
			AnimeID: animeID, Episode: episode, Season: season, Prefs: prefs,
		})
		if err != nil {
			return 0, false, err
		}
		results := found.Results
		if pick != "" {
			results = slices.DeleteFunc(slices.Clone(results), func(r score.Result) bool {
				return !strings.EqualFold(r.Torrent.InfoHash, pick)
			})
			if len(results) == 0 {
				return 0, false, fmt.Errorf("release %s not found", pick)
			}
		}
		if err := addFirst(results); err != nil {
			return 0, false, err
		}
		if added == nil {
			return 0, false, lastErr
		}
		if err := start(true); err != nil {
			return 0, false, err
		}
	}

	switch err := record(ctx); {
	case errors.Is(err, errOvertaken):
		p.log.Info("another release was recorded for the episode meanwhile; this one goes",
			"anime", animeID, "episode", episode)
		p.setTorrent(flight, store.TorrentRecord{})
		discardUnclaimed(context.WithoutCancel(ctx), p.store, p.torrent, p.log, added.ID, best.Torrent.InfoHash)
		return 0, false, err
	case err != nil:
		return added.ID, true, err
	}
	dropSuperseded(ctx, p.store, p.torrent, p.log, animeID, epKey(episode))

	p.log.Info("fetched episode",
		"anime", animeID, "episode", episode, "group", best.Release.Group, "kept", keep)
	return added.ID, true, nil
}
