package library

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
	// Releases resolved ahead of play, in memory only: play uses one to skip the
	// indexer search, and nothing touches the engine until then.
	prepared map[string]preparedRelease
	// Torrents this process started or resumed, which the startup quiet leaves be.
	claimed map[string]bool
}

func (p *Prefetcher) claim(hash string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.claimed == nil {
		p.claimed = map[string]bool{}
	}
	p.claimed[strings.ToLower(hash)] = true
}

func (p *Prefetcher) Claimed(hash string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.claimed[strings.ToLower(hash)]
}

// Resolved as an episode starts, taken when it ends, so it must outlast one.
// Play still warms it, so a stale pick costs one failed candidate.
const preparedTTL = 4 * time.Hour

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

// Opt-in: it shares the line with the episode playing. Failure is silent.
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
				p.log.Debug("prefetch skipped", "anime", animeID, "episode", next, "err", err)
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

// Prepare resolves the episode after this one. Doing the indexer search and
// metadata lookup now makes pressing next immediate later.
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
			// Expired marks are only ever read to be ignored; drop them so a long
			// session does not accumulate one per failed episode.
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

// AwaitPrepare waits for a resolve of this episode already running, so play does
// not race a second search that could pick a different release.
func (p *Prefetcher) AwaitPrepare(ctx context.Context, animeID, episode int) {
	if p == nil {
		return
	}
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
	case <-time.After(prepareWait):
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
	// Already held, so there is nothing to resolve.
	if _, ok := p.held(ctx, animeID, episode); ok {
		return nil
	}

	// A file already in the library plays without a torrent at all, so
	// searching the indexers for it is work with no possible use.
	if f, err := p.store.LocalEpisode(ctx, animeID, episode); err == nil && f.ID != 0 {
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

// TakePrepared returns and forgets a release resolved ahead of time, so play can
// skip the indexer search. A stale one is treated as absent.
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

// Fetch downloads one episode without playing it. Used by auto-download as
// well as prefetch; the only difference is what triggers it.
func (p *Prefetcher) Fetch(ctx context.Context, animeID, episode, season int, prefs score.Preferences) error {
	if p == nil || p.torrent == nil {
		return fmt.Errorf("torrent engine unavailable")
	}
	_, _, err := p.fetch(ctx, animeID, episode, season, prefs, false)
	return err
}

// Download fetches the episode and waits for it to land. Without the wait every
// queued episode is added within seconds and competes for the same line. What
// it lands is kept: asked for, so outside the cache budget and its eviction.
func (p *Prefetcher) Download(ctx context.Context, animeID, episode, season int, prefs score.Preferences, stall time.Duration) error {
	if p == nil || p.torrent == nil {
		return fmt.Errorf("torrent engine unavailable")
	}

	// A copy already held — watched earlier, or cancelled or stalled half way —
	// is promoted and resumed rather than fetched a second time.
	if rec, ok := p.held(ctx, animeID, episode); ok {
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
			return p.awaitOrStop(ctx, id, stall)
		}
	}

	id, started, err := p.fetch(ctx, animeID, episode, season, prefs, true)
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
		if perr := p.torrent.Pause(context.WithoutCancel(ctx), id); perr != nil {
			p.log.Warn("stop abandoned download", "torrent", id, "err", perr)
		}
	}
	return err
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

	p.claim(rec.InfoHash)
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

// How long a queued download may take for its first bytes: a slow swarm's first big piece, not forever.
const prefetchWarmCap = 5 * time.Minute

// fetch reports the torrent id it started and whether it started anything.
// keep puts the episode in the kept tier, which the cache budget does not apply to.
func (p *Prefetcher) fetch(ctx context.Context, animeID, episode, season int, prefs score.Preferences, keep bool) (int, bool, error) {
	if p.inEngine(ctx, animeID, episode) {
		return 0, false, nil
	}

	// Already in the library: downloading a copy of a file on disk is waste.
	if f, err := p.store.LocalEpisode(ctx, animeID, episode); err == nil && f.ID != 0 {
		return 0, false, nil
	}

	// Never let a prefetch push the cache over budget: the episode playing now
	// matters more than the one that might play next.
	var usage store.CacheUsage
	if !keep {
		var err error
		if usage, err = p.store.CacheUsage(ctx); err != nil {
			return 0, false, err
		}
		if usage.Budget > 0 && usage.Bytes >= usage.Budget {
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
			if !cand.AutoPick || tried >= fetchAttempts || cand.Torrent.InfoHash == stale {
				continue
			}
			if !keep && usage.Budget > 0 && usage.Bytes+cand.EpisodeBytes() > usage.Budget {
				return fmt.Errorf("would exceed cache budget")
			}
			tried++

			ins, err := p.torrent.Inspect(ctx, cand.Torrent.Magnet())
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

	start := func() error {
		p.claim(best.Torrent.InfoHash)
		err := p.torrent.WaitLive(ctx, added.ID, 2*time.Minute)
		if err == nil {
			// The stream client waits as long as its context.
			warm, cancel := context.WithTimeout(ctx, prefetchWarmCap)
			err = p.torrent.Prewarm(warm, added.ID, index, 2<<20)
			cancel()
		}
		if err != nil {
			// Cancelled or dead half way: stopped, not left fetching unrecorded.
			if perr := p.torrent.Pause(context.WithoutCancel(ctx), added.ID); perr != nil {
				p.log.Warn("stop abandoned download", "torrent", added.ID, "err", perr)
			}
		}
		return err
	}

	// Resolved while the episode before it downloaded: tried first, and the search is the fallback.
	p.AwaitPrepare(ctx, animeID, episode)
	if rel, ok := p.TakePrepared(animeID, episode); ok {
		if err := addFirst([]score.Result{rel}); err != nil {
			return 0, false, err
		}
		if added != nil {
			err := start()
			if err != nil && ctx.Err() != nil {
				return 0, false, err
			}
			if err != nil {
				p.log.Info("release resolved ahead did not start, searching", "anime", animeID, "episode", episode)
				// Replaced, not retried: left paused it would sit in the downloads list under no show.
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
		if err := addFirst(found.Results); err != nil {
			return 0, false, err
		}
		if added == nil {
			return 0, false, lastErr
		}
		if err := start(); err != nil {
			return 0, false, err
		}
	}

	if err := p.store.RecordTorrent(ctx, store.TorrentRecord{
		InfoHash:  best.Torrent.InfoHash,
		EngineID:  added.ID,
		Name:      inspected.Details.Name,
		TotalSize: file.Length,
		AnimeID:   animeID,
		EpKey:     epKey(episode),
		FileIndex: index,
		FilePath:  file.Name,
	}); err != nil {
		return added.ID, true, err
	}
	dropSuperseded(ctx, p.store, p.torrent, p.log, animeID, epKey(episode))

	// A prefetched episode is not playing, so it must not hold a pin: the
	// cache manager has to stay free to evict it under pressure.
	if err := p.store.PinCache(ctx, best.Torrent.InfoHash, index, false); err != nil {
		return added.ID, true, err
	}
	if keep {
		if _, err := p.store.KeepDownload(ctx, best.Torrent.InfoHash, true); err != nil {
			return added.ID, true, err
		}
	}

	p.log.Info("fetched episode",
		"anime", animeID, "episode", episode, "group", best.Release.Group, "kept", keep)
	return added.ID, true, nil
}
