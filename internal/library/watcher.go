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
	if !prefs.Bool("notify.enabled") {
		return rep, nil
	}
	want := audioFilter(prefs.String("notify.releases"))

	follows, err := w.store.Follows(ctx)
	if err != nil {
		return rep, err
	}

	for _, f := range follows {
		rep.Checked++

		next := f.Progress + 1
		if f.Episodes > 0 && next > f.Episodes {
			continue
		}
		// Something numbered ahead of the broadcast turns up most weeks; it is
		// never the episode, and downloading it fails after announcing it.
		if !f.Aired(next, time.Now().Unix()) {
			continue
		}

		found, err := w.finder.Find(ctx, Request{
			AnimeID: f.AnimeID,
			Episode: next,
			// Season left to the finder, which reads it from the title.
			Prefs: w.prefsFor(ctx, f.AnimeID),
		})
		if err != nil {
			w.log.Warn("watch poll", "anime", f.AnimeID, "err", err)
			continue
		}

		for _, r := range found.Results {
			// Confirmed only: a pack that states no range is a guess, and
			// announcing one claims an episode nobody has released.
			if !r.AutoPick || !r.Confirmed || !want(r.Release) {
				continue
			}
			if seen, _ := w.store.AlreadyNotified(ctx, r.Torrent.InfoHash, f.AnimeID, next); seen {
				continue
			}
			rep.Found++

			if err := w.notify(ctx, f.AnimeID, next, r.Torrent.Title, r.Torrent.InfoHash); err != nil {
				w.log.Warn("notify", "err", err)
				continue
			}
			rep.Notified++

			if w.download(ctx, f.AnimeID, next, prefs) {
				rep.Downloaded++
			}
			break
		}
	}
	return rep, nil
}

// Per-anime prefs overlay the global switch. Queued rather than fetched so many
// shows updating at once don't start that many downloads, behind manual requests.
func (w *Watcher) download(ctx context.Context, animeID, episode int, _ store.Prefs) bool {
	if w.queue == nil {
		return false
	}

	prefs, err := w.store.Prefs(ctx, animeID)
	if err != nil || !prefs.Bool("autodownload.enabled") {
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

// Run polls on an interval until the context is cancelled.
func (w *Watcher) Run(ctx context.Context) {
	for {
		prefs, err := w.store.Prefs(ctx, 0)
		interval := 30 * time.Minute
		if err == nil {
			if secs := prefs.Int("notify.poll_seconds"); secs > 60 {
				interval = time.Duration(secs) * time.Second
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}

		if rep, err := w.Poll(ctx); err != nil {
			w.log.Warn("release watch", "err", err)
		} else if rep.Notified > 0 {
			w.log.Info("new releases", "notified", rep.Notified, "checked", rep.Checked)
		}
	}
}
