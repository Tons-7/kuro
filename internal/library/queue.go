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

// Downloader works the queue one episode at a time across shows: several at once all finish later.
type Downloader struct {
	store    *store.Store
	prefetch *Prefetcher
	// A show's own preferences, over the global ones.
	prefs func(ctx context.Context, animeID int) score.Preferences
	log   *slog.Logger

	// Woken on a new entry so a queued episode starts at once rather than on the next tick.
	wake chan struct{}

	mu      sync.Mutex
	current *inFlight
	// Paused from the downloads list: the only pause that drops a queued episode.
	userPaused map[string]bool
	// Stream sessions watching now; on a slow line the queue waits for them.
	watching map[string]struct{}
	// Paused as a whole from the downloads list: nothing starts until it is resumed. Kept across restarts.
	paused bool
}

// pausedSetting remembers a paused queue, so a restart does not start downloading again by itself.
const pausedSetting = "downloads.paused"

// Paused reports a queue the user stopped as a whole.
func (d *Downloader) Paused() bool {
	if d == nil {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.paused
}

// SetPaused stops or restarts the whole queue. Pausing sends the episode in flight back to wait its turn.
func (d *Downloader) SetPaused(ctx context.Context, paused bool) error {
	if d == nil {
		return nil
	}
	if err := d.store.SetSetting(ctx, pausedSetting, fmt.Sprint(paused)); err != nil {
		return err
	}
	d.mu.Lock()
	d.paused = paused
	current := d.current
	d.mu.Unlock()

	if paused && current != nil {
		d.log.Info("download queue paused")
		current.stop()
	}
	if !paused {
		d.Wake()
	}
	return nil
}

// restorePaused picks up a pause from before a restart.
func (d *Downloader) restorePaused(ctx context.Context) {
	if v, err := d.store.Setting(ctx, pausedSetting); err == nil && v == "true" {
		d.mu.Lock()
		d.paused = true
		d.mu.Unlock()
		d.log.Info("download queue is paused; resume it from Downloads")
	}
}

// Now puts a waiting episode first and starts it at once: the one in flight goes back to wait behind it.
// Asking for a download is asking for the queue to run, so a paused queue resumes.
func (d *Downloader) Now(ctx context.Context, animeID int, epKey string) (bool, error) {
	if d == nil {
		return false, nil
	}
	moved, err := d.store.Prioritise(ctx, animeID, epKey)
	if err != nil || !moved {
		return moved, err
	}
	d.mu.Lock()
	current := d.current
	d.mu.Unlock()
	if current != nil && (current.animeID != animeID || current.epKey != epKey) {
		current.stop()
	}
	return true, d.SetPaused(ctx, false)
}

// Restart is Now for an episode whose release was just changed: at the front, and stopped if it is the
// one downloading, so the worker takes it up again with the release chosen.
func (d *Downloader) Restart(ctx context.Context, animeID int, epKey string) (bool, error) {
	if d == nil {
		return false, nil
	}
	moved, err := d.store.Promote(ctx, animeID, epKey)
	if err != nil || !moved {
		return moved, err
	}
	d.mu.Lock()
	current := d.current
	d.mu.Unlock()
	if current != nil {
		current.stop()
	}
	return true, d.SetPaused(ctx, false)
}

// inFlight is what the worker fetches, so a cancel reaches it before the database names it.
type inFlight struct {
	animeID int
	epKey   string
	stop    context.CancelFunc
}

// session is the stream session id this episode would play under, as streamOpen builds it.
func (f *inFlight) session() string {
	return fmt.Sprintf("%d-%s", f.animeID, f.epKey)
}

func NewDownloader(s *store.Store, p *Prefetcher, prefs func(context.Context, int) score.Preferences, log *slog.Logger) *Downloader {
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

// Downloading: the queue fetches this episode now, so closing a player doesn't pause it.
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

// Held reports a queue standing down: paused, or an episode is playing.
func (d *Downloader) Held() bool { return d.held() }

func (d *Downloader) held() bool {
	if d == nil {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.paused || len(d.watching) > 0
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

// Shrunk in tests.
var (
	queueIdle = 30 * time.Second

	// A swarm with no seeders never finishes or errors. Ten idle minutes survives
	// a bad patch on a slow line without a dead release holding up the queue.
	queueStall = 10 * time.Minute

	// A dead swarm is tried again later, twice, further apart each time: peers come and go, and a
	// release that stalled at night often finishes in the morning.
	queueRetries    = 2
	queueRetryDelay = 5 * time.Minute
)

// Re-check cadence for torrents the engine was still verifying at startup.
var (
	quietRecheck = 10 * time.Second
	quietFor     = 15 * time.Minute
)

// Quiet pauses the torrents resumed on launch, which would all download at once; kept episodes finish.
func (d *Downloader) Quiet(ctx context.Context) {
	if d == nil || d.prefetch == nil || d.prefetch.torrent == nil {
		return
	}
	pending := d.quiet(ctx, nil)
	if len(pending) == 0 {
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
			// Only what was verifying at launch; the rest was started since.
			if pending = d.quiet(ctx, pending); len(pending) == 0 {
				return
			}
		}
	}()
}

// quiet is one pass over only (all when nil); it returns those still checking.
func (d *Downloader) quiet(ctx context.Context, only map[string]bool) map[string]bool {
	var started map[string]bool
	if prefs, err := d.store.Prefs(ctx, 0); err == nil && prefs.Bool("cache.prefetch_full") {
		if started, err = d.store.StartedTorrents(ctx); err != nil {
			d.log.Warn("list started downloads", "err", err)
		}
	}
	pinned := map[string]bool{}
	if entries, err := d.store.CacheEntries(ctx); err == nil {
		for _, e := range entries {
			if e.Pinned {
				pinned[strings.ToLower(e.InfoHash)] = true
			}
		}
	}
	keep := func(hash string) bool {
		return started[hash] || pinned[hash] || d.prefetch.Claimed(hash) || (only != nil && !only[hash])
	}

	paused, kept, checking, err := d.prefetch.torrent.PauseUnfinished(ctx, keep)
	if err != nil {
		d.log.Warn("quiet the torrent engine", "err", err)
		return nil
	}
	if paused > 0 || len(checking) > 0 {
		d.log.Info("part-downloaded torrents on startup",
			"paused", paused, "leftDownloading", kept, "stillChecking", len(checking))
	}
	still := map[string]bool{}
	for _, hash := range checking {
		if only == nil || only[hash] {
			still[hash] = true
		}
	}
	return still
}

func (d *Downloader) Run(ctx context.Context) {
	if d == nil || d.prefetch == nil {
		return
	}

	// Anything the last run was working on when it stopped.
	if err := d.store.ResetActive(ctx); err != nil {
		d.log.Warn("reset download queue", "err", err)
	}
	d.restorePaused(ctx)

	for {
		worked := !d.held() && d.step(ctx)
		if ctx.Err() != nil {
			return
		}
		// Straight on while there is work, else wait to be woken instead of polling.
		if worked {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-d.wake:
		case <-time.After(d.idle(ctx)):
		}
	}
}

// idle is how long to wait when nothing is due: the usual pause, or until a deferred try comes round.
func (d *Downloader) idle(ctx context.Context) time.Duration {
	at, ok, err := d.store.NextRetry(ctx)
	if err != nil || !ok {
		return queueIdle
	}
	return max(time.Millisecond, min(queueIdle, time.Until(at)))
}

// swarmFailure: the release may be fine and its peers gone for now, which is what a later try is for.
func swarmFailure(err error) bool {
	var stall *torrent.StallError
	return errors.Is(err, ErrSeedersSilent) || errors.As(err, &stall)
}

// describe says a failure in words the downloads list can show as they are.
func describe(err error) error {
	var stall *torrent.StallError
	switch {
	case errors.As(err, &stall):
		minutes := max(1, int(stall.Quiet.Round(time.Minute).Minutes()))
		return fmt.Errorf("no peers sent anything for %d minute%s; %.0f%% is on disk",
			minutes, plural(minutes), stall.Percent)
	case errors.Is(err, ErrSeedersSilent):
		return ErrSeedersSilent
	}
	return err
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
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

// SetUserPaused records a pause or resume from the downloads list.
func (d *Downloader) SetUserPaused(hash string, paused bool) {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.userPaused == nil {
		d.userPaused = map[string]bool{}
	}
	if paused {
		d.userPaused[strings.ToLower(hash)] = true
	} else {
		delete(d.userPaused, strings.ToLower(hash))
	}
}

func (d *Downloader) pausedByUser(ctx context.Context, animeID int, ep string) bool {
	rec, ok, err := d.store.TorrentForEpisode(ctx, animeID, ep)
	if err != nil || !ok {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.userPaused[strings.ToLower(rec.InfoHash)]
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

	// One ahead, so its release is ready when this one ends; not one whose release was chosen.
	if after, ok, err := d.store.PeekQueued(parent); err == nil && ok && after.InfoHash == "" {
		d.prefetch.Ahead(after.AnimeID, after.Episode, after.Season, d.prefs(parent, after.AnimeID))
	}

	ctx, stop := context.WithCancel(parent)
	defer stop()

	d.mu.Lock()
	d.current = &inFlight{animeID: next.AnimeID, epKey: next.EpKey, stop: stop}
	// A stream opened or a pause landed since Run looked: stood down at once, back in line.
	if d.paused || len(d.watching) > 0 {
		stop()
	}
	d.mu.Unlock()

	err = d.prefetch.DownloadRelease(ctx, next.AnimeID, next.Episode, next.Season, d.prefs(parent, next.AnimeID), queueStall, next.InfoHash)

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
	// and move on rather than leaving a red row behind. Any other pause requeues.
	case errors.Is(err, torrent.ErrPaused):
		if !d.pausedByUser(parent, next.AnimeID, next.EpKey) {
			if _, err := d.store.RequeueActive(parent, next.AnimeID, next.EpKey); err != nil {
				d.log.Warn("requeue paused download", "err", err)
			}
			return true
		}
		d.log.Info("queued download paused",
			"anime", next.AnimeID, "episode", next.Episode)
		err = nil

	case err != nil && swarmFailure(err) && next.Attempts < queueRetries:
		wait := queueRetryDelay * time.Duration(next.Attempts+1)
		d.log.Info("the swarm gave nothing; trying again later",
			"anime", next.AnimeID, "episode", next.Episode, "in", wait, "reason", err)
		if err := d.store.Defer(parent, next.AnimeID, next.EpKey, describe(err), time.Now().Add(wait)); err != nil {
			d.log.Warn("defer queued download", "err", err)
		}
		return true

	case err != nil:
		d.log.Warn("queued download failed",
			"anime", next.AnimeID, "episode", next.Episode, "err", err)
		err = describe(err)
		if next.Attempts > 0 {
			err = fmt.Errorf("gave up after %d tries: %w", next.Attempts+1, err)
		}
	}

	if err := d.store.FinishQueued(parent, next.AnimeID, next.EpKey, err); err != nil {
		d.log.Warn("finish queued download", "err", err)
	}
	return true
}
