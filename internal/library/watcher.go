package library

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"kuro/internal/indexer"
	"kuro/internal/parse"
	"kuro/internal/store"
)

// Watcher polls followed shows for new episodes, raises notifications, and
// optionally grabs them.
type Watcher struct {
	store   *store.Store
	finder  *Finder
	indexer indexer.Source
	queue   *Downloader
	log     *slog.Logger
}

func NewWatcher(s *store.Store, f *Finder, idx indexer.Source, log *slog.Logger) *Watcher {
	return &Watcher{store: s, finder: f, indexer: idx, log: log}
}

// WithDownloader enables auto-download. Without it the watcher only notifies.
func (w *Watcher) WithDownloader(d *Downloader) *Watcher { w.queue = d; return w }

type WatchReport struct {
	Checked    int `json:"checked"`
	Found      int `json:"found"`
	Notified   int `json:"notified"`
	Downloaded int `json:"downloaded"`
}

func (w *Watcher) Poll(ctx context.Context) (WatchReport, error) {
	var rep WatchReport

	prefs, err := w.store.Prefs(ctx, 0)
	if err != nil {
		return rep, err
	}
	// Notifications and auto-download are separate; either is reason to look.
	notifyOn := prefs.Bool("notify.enabled")
	want := audioFilter(prefs.String("notify.releases"))

	follows, err := w.store.Follows(ctx)
	if err != nil {
		return rep, err
	}

	for _, f := range follows {
		auto := w.autoDownload(ctx, f.AnimeID)
		if !notifyOn && !auto {
			continue
		}
		rep.Checked++

		// Every aired episode past progress, not just the next one: someone
		// saving a season for later still wants each week announced. Capped so
		// a long backlog does not flood the indexers in one poll.
		now := time.Now().Unix()
		searched := 0
		for next := f.Progress + 1; searched < watchAhead && next <= f.Progress+maxBacklog; next++ {
			if f.Episodes > 0 && next > f.Episodes {
				break
			}
			// Something numbered ahead of the broadcast turns up most weeks; it
			// is never the episode, and downloading it fails after announcing it.
			if !f.Aired(next, now) {
				break
			}
			if seen, _ := w.store.AlreadyNotified(ctx, "", f.AnimeID, next); seen {
				continue
			}
			searched++
			w.check(ctx, f.AnimeID, next, notifyOn, auto, want, &rep)
		}
	}
	return rep, nil
}

// Searches per show per poll, and how far past progress to look at all.
const (
	watchAhead = 4
	maxBacklog = 200
)

func (w *Watcher) check(ctx context.Context, animeID, episode int, notifyOn, auto bool, want func(parse.Release) bool, rep *WatchReport) {
	found, err := w.finder.Find(ctx, Request{
		AnimeID: animeID,
		Episode: episode,
		// Season left to the finder, which reads it from the title.
		Prefs: w.prefsFor(ctx, animeID),
	})
	if err != nil {
		w.log.Warn("watch poll", "anime", animeID, "episode", episode, "err", err)
		return
	}

	for _, r := range found.Results {
		// Confirmed only: a pack that states no range is a guess, and
		// announcing one claims an episode nobody has released.
		if !r.AutoPick || !r.Confirmed || !want(r.Release) {
			continue
		}
		if seen, _ := w.store.AlreadyNotified(ctx, r.Torrent.InfoHash, animeID, episode); seen {
			continue
		}
		rep.Found++

		if !notifyOn {
			if err := w.store.RecordNotified(ctx, r.Torrent.InfoHash, animeID, episode); err != nil {
				w.log.Warn("record release", "err", err)
			}
		} else if err := w.notify(ctx, animeID, episode, r.Torrent.Title, r.Torrent.InfoHash); err != nil {
			w.log.Warn("notify", "err", err)
		} else {
			rep.Notified++
		}

		if auto && w.download(ctx, animeID, episode) {
			rep.Downloaded++
		}
		return
	}
}

// Per-anime prefs overlay the global switch.
func (w *Watcher) autoDownload(ctx context.Context, animeID int) bool {
	prefs, err := w.store.Prefs(ctx, animeID)
	return err == nil && prefs.Bool("autodownload.enabled")
}

// Queued rather than fetched so many shows updating at once don't start that
// many downloads, behind manual requests.
func (w *Watcher) download(ctx context.Context, animeID, episode int) bool {
	if w.queue == nil {
		return false
	}
	// Season 0: the finder derives it from the title, as the manual path does.
	// Forcing 1 makes it reject every season-2 release it just announced.
	if _, err := w.store.Enqueue(ctx, animeID, 0, []int{episode}); err != nil {
		w.log.Warn("auto-download", "anime", animeID, "episode", episode, "err", err)
		return false
	}
	w.queue.Wake()

	w.log.Info("auto-download queued", "anime", animeID, "episode", episode)
	return true
}

func (w *Watcher) notify(ctx context.Context, animeID, episode int, title, infoHash string) error {
	if _, err := w.store.AddNotification(ctx, store.Notification{
		Kind:    store.NotifyRelease,
		AnimeID: &animeID,
		Episode: &episode,
		Title:   fmt.Sprintf("Episode %d is available", episode),
		Body:    title,
		Payload: map[string]any{"infoHash": infoHash},
	}); err != nil {
		return err
	}
	return w.store.RecordNotified(ctx, infoHash, animeID, episode)
}

func (w *Watcher) prefsFor(ctx context.Context, animeID int) scorePrefs {
	p, err := w.store.Prefs(ctx, animeID)
	if err != nil {
		return defaultScorePrefs()
	}
	return prefsFromSettings(p)
}

// A dual-audio release satisfies both preferences: the Japanese and English
// tracks are both in the file and chosen at playback.
func audioFilter(pref string) func(parse.Release) bool {
	switch strings.ToLower(pref) {
	case "dub":
		return func(r parse.Release) bool {
			return r.DualAudio || mentionsDub(r)
		}
	case "either", "both":
		return func(parse.Release) bool { return true }
	default:
		// Subtitled releases are the default and carry no marker, so only an
		// explicitly dub-only release is excluded.
		return func(r parse.Release) bool {
			return r.DualAudio || !mentionsDub(r)
		}
	}
}

// Reads the original filename: the parser strips bracketed tags from Title, so
// a "[Dual Audio]" or "[English Dub]" marker is gone by then.
func mentionsDub(r parse.Release) bool {
	lower := strings.ToLower(r.Raw)
	return strings.Contains(lower, "dub") || strings.Contains(lower, "english audio")
}
