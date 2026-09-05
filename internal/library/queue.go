package library

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"kuro/internal/score"
	"kuro/internal/store"
	"kuro/internal/torrent"
)

// Downloader works the queue one episode at a time, across every show, since
// several at once share one connection and all finish later.
type Downloader struct {
	store    *store.Store
	prefetch *Prefetcher
	prefs    func(context.Context) score.Preferences
	log      *slog.Logger

	// Woken on a new entry so a queued episode starts at once rather than on
	// the next tick.
	wake chan struct{}

	mu      sync.Mutex
	current *inFlight
	// Stream sessions currently watching. A queued download shares the on-screen
	// episode's connection, so on a slow line the queue waits.
	watching map[string]struct{}
}

// inFlight is what the worker is downloading, so cancelling can reach it — the
// database has no row naming the torrent until a minute in.
type inFlight struct {
	animeID int
	epKey   string
	stop    context.CancelFunc
}

// session is the stream session id this episode would be watched under, built
// the same way streamOpen builds it.
func (f *inFlight) session() string {
	return fmt.Sprintf("%d-%s", f.animeID, f.epKey)
}

func NewDownloader(s *store.Store, p *Prefetcher, prefs func(context.Context) score.Preferences, log *slog.Logger) *Downloader {
	return &Downloader{
		store:    s,
		prefetch: p,
		prefs:    prefs,
		log:      log,
		wake:     make(chan struct{}, 1),
		watching: map[string]struct{}{},
	}
}

// Hold makes the queue stand down while a session is watching. Keyed by session
// so repeated opens count once and a vanished session cannot hold it forever.
func (d *Downloader) Hold(sessionID string) {
	if d == nil {
		return
	}
	d.mu.Lock()
	_, already := d.watching[sessionID]
	d.watching[sessionID] = struct{}{}
	current := d.current
	d.mu.Unlock()

	if already || current == nil {
		return
	}

	// If the queue is already fetching the watched episode, that download *is* the
	// buffering; stopping it would stall playback reading from it.
	if current.session() == sessionID {
		return
	}

	// Otherwise stop the episode in flight rather than waiting for it: the point
	// is to give the connection back now. Its entry goes back to pending.
	d.log.Info("pausing the queue while an episode plays", "session", sessionID)
	current.stop()
}

// Downloading reports that the queue is fetching this episode right now, so
// closing a player is not a reason to pause it.
func (d *Downloader) Downloading(sessionID string) bool {
	if d == nil {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.current != nil && d.current.session() == sessionID
}

// Release gives the queue back once nothing is watching.
func (d *Downloader) Release(sessionID string) {
	if d == nil {
		return
	}
	d.mu.Lock()
	delete(d.watching, sessionID)
	idle := len(d.watching) == 0
	d.mu.Unlock()

	if idle {
		d.Wake()
	}
}

func (d *Downloader) held() bool {
	if d == nil {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.watching) > 0
}

// Wake tells the worker there is something to do.
func (d *Downloader) Wake() {
	if d == nil {
		return
	}
	select {
	case d.wake <- struct{}{}:
	default:
	}
}

const (
	queueIdle = 30 * time.Second

	// A swarm with no seeders never finishes or errors. Ten idle minutes survives
	// a bad patch on a slow line without a dead release holding up the queue.
	queueStall = 10 * time.Minute
)

// Re-check cadence for torrents rqbit was still verifying at startup.
var (
	quietRecheck = 10 * time.Second
	quietFor     = 15 * time.Minute
)

// Quiet pauses the torrents rqbit auto-resumed on launch, which would otherwise
// all download at once; a "keep whole episode" one is left to finish. Ones
// still being verified are revisited once the check ends.
func (d *Downloader) Quiet(ctx context.Context) {
	if d == nil || d.prefetch == nil || d.prefetch.torrent == nil {
		return
	}
	if d.quiet(ctx) == 0 {
		return
	}
	recheck := quietRecheck
	go func() {
		deadline := time.Now().Add(quietFor)
		for time.Now().Before(deadline) {
			select {
			case <-ctx.Done():
				return
			case <-time.After(recheck):
			}
			if d.quiet(ctx) == 0 {
				return
			}
		}
	}()
}

// quiet is one pass; it reports how many torrents were still checking.
func (d *Downloader) quiet(ctx context.Context) int {
	var keep func(string) bool
	if prefs, err := d.store.Prefs(ctx, 0); err == nil && prefs.Bool("cache.prefetch_full") {
		started, err := d.store.StartedTorrents(ctx)
		if err != nil {
			d.log.Warn("list started downloads", "err", err)
		}
		keep = func(hash string) bool { return started[hash] }
	}

	paused, kept, checking, err := d.prefetch.torrent.PauseUnfinished(ctx, keep)
	if err != nil {
		d.log.Warn("quiet the torrent engine", "err", err)
		return 0
	}
	if paused > 0 || kept > 0 || checking > 0 {
		d.log.Info("part-downloaded torrents on startup",
			"paused", paused, "leftDownloading", kept, "stillChecking", checking)
	}
	return checking
}

func (d *Downloader) Run(ctx context.Context) {
	if d == nil || d.prefetch == nil {
		return
	}

	// Anything the last run was working on when it stopped.
	if err := d.store.ResetActive(ctx); err != nil {
		d.log.Warn("reset download queue", "err", err)
	}

	for {
		worked := !d.held() && d.step(ctx)
		if ctx.Err() != nil {
			return
		}
		// Straight on to the next while there is work; otherwise wait to be
		// woken rather than polling the database every second.
		if worked {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-d.wake:
		case <-time.After(queueIdle):
		}
	}
}

// Cancel drops a show's queued episodes and stops the one in flight; an empty
// epKey cancels the whole show. Cancelling its context ends the wait in step.
func (d *Downloader) Cancel(ctx context.Context, animeID int, epKey string) (int, error) {
	if d == nil {
		return 0, nil
	}

	removed, err := d.store.Dequeue(ctx, animeID, epKey)
	if err != nil {
		return 0, err
	}

	d.mu.Lock()
	if c := d.current; c != nil && c.animeID == animeID && (epKey == "" || c.epKey == epKey) {
		c.stop()
		removed++
	}
	d.mu.Unlock()

	return removed, nil
}

func (d *Downloader) step(parent context.Context) bool {
	next, ok, err := d.store.NextQueued(parent)
	if err != nil {
		d.log.Warn("read download queue", "err", err)
		return false
	}
	if !ok {
		return false
	}

	d.log.Info("downloading queued episode",
		"anime", next.AnimeID, "episode", next.Episode)

	ctx, stop := context.WithCancel(parent)
	defer stop()

	d.mu.Lock()
	d.current = &inFlight{animeID: next.AnimeID, epKey: next.EpKey, stop: stop}
	d.mu.Unlock()

	err = d.prefetch.Download(ctx, next.AnimeID, next.Episode, next.Season, d.prefs(parent), queueStall)

	d.mu.Lock()
	d.current = nil
	d.mu.Unlock()

	switch {
	// Stopped part-way by a cancel or a play starting. A cancel already deleted
	// the row, so requeuing what's left distinguishes the two and resumes a hold.
	case ctx.Err() != nil && parent.Err() == nil:
		requeued, err := d.store.RequeueActive(parent, next.AnimeID, next.EpKey)
		if err != nil {
			d.log.Warn("requeue held download", "err", err)
		}
		if requeued {
			d.log.Info("queue stood down, episode back in line",
				"anime", next.AnimeID, "episode", next.Episode)
		} else {
			d.log.Info("queued download cancelled",
				"anime", next.AnimeID, "episode", next.Episode)
		}
		return true

	// Pausing one from the downloads list is a decision, not a failure: drop it
	// and move on rather than leaving a red row behind.
	case errors.Is(err, torrent.ErrPaused):
		d.log.Info("queued download paused",
			"anime", next.AnimeID, "episode", next.Episode)
		err = nil

	case err != nil:
		d.log.Warn("queued download failed",
			"anime", next.AnimeID, "episode", next.Episode, "err", err)
	}

	if err := d.store.FinishQueued(parent, next.AnimeID, next.EpKey, err); err != nil {
		d.log.Warn("finish queued download", "err", err)
	}
	return true
}
