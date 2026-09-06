package library

import (
	"context"
	"io"
	"log/slog"
	"os"
	"regexp"
	"testing"

	"kuro/internal/anilist"
	"kuro/internal/store"
)

// TestLiveWatcherAnnouncesTheEpisodeThatAired follows Bleach TYBW: The Calamity
// at progress 6 and polls the real indexers: episode 7 (aired) is announced
// with a release of this cour, episode 8 (not yet) is not.
// Opt-in: KURO_LIVE=1 KURO_NYAA=… go test ./internal/library -run LiveWatcher -v
func TestLiveWatcherAnnouncesTheEpisodeThatAired(t *testing.T) {
	if os.Getenv("KURO_LIVE") == "" {
		t.Skip("set KURO_LIVE=1 to search the live indexers")
	}
	const show, watched = 185874, 6

	st := prefetchStore(t)
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	al := anilist.New(log)
	seedLikeTheApp(t, st, al, log, show)
	if _, err := st.MarkWatched(ctx, show, watched); err != nil {
		t.Fatal(err)
	}
	if err := st.SetFollow(ctx, store.Follow{AnimeID: show}, true); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(ctx, "notify.enabled", "true"); err != nil {
		t.Fatal(err)
	}

	sources := liveSources(t)
	w := NewWatcher(st, NewFinder(st, sources, log), sources, log)
	rep, err := w.Poll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("report: %+v", rep)

	notes, err := st.Notifications(ctx, false, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 {
		t.Fatalf("got %d notifications, want one for episode %d: %+v", len(notes), watched+1, notes)
	}
	n := notes[0]
	t.Logf("announced: %s — %s", n.Title, n.Body)
	if n.Episode == nil || *n.Episode != watched+1 {
		t.Errorf("announced episode %v, want %d", n.Episode, watched+1)
	}
	if !regexp.MustCompile(`(?i)kashin|calamity|e?47\b`).MatchString(n.Body) ||
		regexp.MustCompile(`(?i)soukoku|conflict|ketsubetsu`).MatchString(n.Body) {
		t.Errorf("announced another cour's release: %s", n.Body)
	}

	// Polling again announces nothing new.
	if rep, err := w.Poll(ctx); err != nil || rep.Notified != 0 {
		t.Errorf("second poll notified %d (err %v), want 0", rep.Notified, err)
	}
}
