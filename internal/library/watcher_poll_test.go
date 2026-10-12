package library

import (
	"context"
	"testing"
	"time"

	"kuro/internal/indexer"
	"kuro/internal/store"
)

// A show on the list as watching, with its status synced from the tracker rather than set here.
// Episode 5 airs at `airs`; everything before has aired.
func watchedShow(t *testing.T, airs time.Time) *store.Store {
	t.Helper()
	st := prefetchStore(t)
	setAiring(t, st, 5, airs)
	return st
}

func setAiring(t *testing.T, st *store.Store, nextEpisode int, airs time.Time) {
	t.Helper()
	ctx := context.Background()
	episodes, current, planning := 12, "CURRENT", "PLANNING"
	at := int(airs.Unix())
	if _, err := st.ImportList(ctx,
		[]store.Anime{
			{ID: 1, Romaji: "Show", Synonyms: "[]", Genres: "[]", Episodes: &episodes, NextEpisode: &nextEpisode, NextAiringAt: &at},
			{ID: 2, Romaji: "Later", Synonyms: "[]", Genres: "[]", Episodes: &episodes},
		},
		[]store.Entry{{ID: 11, AnimeID: 1, Status: &current, Progress: 3}, {ID: 12, AnimeID: 2, Status: &planning}},
		store.ImportMerge); err != nil {
		t.Fatal(err)
	}
}

func notifications(t *testing.T, st *store.Store) []store.Notification {
	t.Helper()
	items, err := st.Notifications(context.Background(), false, 50)
	if err != nil {
		t.Fatal(err)
	}
	return items
}

func watcherWith(st *store.Store, releases ...indexer.Torrent) *Watcher {
	idx := fixedIndexer{results: releases}
	return NewWatcher(st, NewFinder(st, idx, discard()), idx, discard())
}

// What had aired before the first look is old news; the episode that airs afterwards is announced, once.
func TestWatcherAnnouncesOnlyWhatAirsAfterItStartedLooking(t *testing.T) {
	st := prefetchStore(t)
	// Nothing past the viewer has aired yet: episode 4 is next, in an hour.
	setAiring(t, st, 4, time.Now().Add(time.Hour))
	ctx := context.Background()
	w := watcherWith(st,
		release(goodHash, "[Best] Show - 04 [1080p].mkv", 500),
		release(deadHash, "[Best] Show - 05 [1080p].mkv", 500),
		release("3333333333333333333333333333333333333333", "[Best] Later - 01 [1080p].mkv", 500),
	)

	rep, err := w.Poll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Checked != 1 || rep.Notified != 0 || len(notifications(t, st)) != 0 {
		t.Fatalf("the first look announced old episodes: %+v", rep)
	}

	// Episode 4 airs, and 5 is next.
	setAiring(t, st, 5, time.Now().Add(time.Hour))
	rep, _ = w.Poll(ctx)
	got := notifications(t, st)
	if rep.Notified != 1 || len(got) != 1 || got[0].Episode == nil || *got[0].Episode != 4 {
		t.Fatalf("report = %+v, notifications = %+v; want episode 4 announced", rep, got)
	}
	if rep, _ := w.Poll(ctx); rep.Notified != 0 || len(notifications(t, st)) != 1 {
		t.Fatalf("a later poll announced it again: %+v", rep)
	}
}

// A show that finished long ago has nothing new, however far behind the viewer is.
func TestWatcherIsSilentAboutAShowFinishedLongAgo(t *testing.T) {
	st := prefetchStore(t)
	ctx := context.Background()
	episodes, current := 12, "CURRENT"
	if _, err := st.ImportList(ctx,
		[]store.Anime{{ID: 1, Romaji: "Show", Synonyms: "[]", Genres: "[]", Episodes: &episodes}},
		[]store.Entry{{ID: 11, AnimeID: 1, Status: &current, Progress: 3}}, store.ImportMerge); err != nil {
		t.Fatal(err)
	}
	w := watcherWith(st, release(goodHash, "[Best] Show - 04 [1080p].mkv", 500), release(deadHash, "[Best] Show - 09 [1080p].mkv", 500))
	for range 3 {
		if rep, _ := w.Poll(ctx); rep.Notified != 0 || len(notifications(t, st)) != 0 {
			t.Fatalf("an old episode was announced: %+v", rep)
		}
	}
}

// A show muted on its own page is left alone while the rest are announced.
func TestWatcherSkipsAShowMutedOnItsPage(t *testing.T) {
	st := watchedShow(t, time.Now().Add(-time.Hour))
	ctx := context.Background()
	if err := st.SetAnimePref(ctx, 1, "notify.enabled", "false"); err != nil {
		t.Fatal(err)
	}
	w := watcherWith(st, release(goodHash, "[Best] Show - 04 [1080p].mkv", 500))
	if rep, _ := w.Poll(ctx); rep.Checked != 0 || len(notifications(t, st)) != 0 {
		t.Fatalf("a muted show was checked: %+v", rep)
	}
}

func TestWatcherIsQuietWhenNotificationsAreOff(t *testing.T) {
	st := watchedShow(t, time.Now().Add(-time.Hour))
	ctx := context.Background()
	if err := st.SetSetting(ctx, "notify.enabled", "false"); err != nil {
		t.Fatal(err)
	}
	w := watcherWith(st, release(goodHash, "[Best] Show - 04 [1080p].mkv", 500))
	if rep, _ := w.Poll(ctx); rep.Checked != 0 || len(notifications(t, st)) != 0 {
		t.Fatalf("notifications off, yet %+v", rep)
	}
}
