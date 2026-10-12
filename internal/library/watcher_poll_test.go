package library

import (
	"context"
	"fmt"
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

func episodeOf(n store.Notification) int {
	if n.Episode == nil {
		return 0
	}
	return *n.Episode
}

// Nothing past the viewer has aired, so there is no news; once an episode airs it is announced, once.
func TestWatcherAnnouncesAnEpisodeOnceItAirs(t *testing.T) {
	st := prefetchStore(t)
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
		t.Fatalf("announced before anything aired: %+v", rep)
	}

	// Episode 4 airs, and 5 is next.
	setAiring(t, st, 5, time.Now().Add(time.Hour))
	rep, _ = w.Poll(ctx)
	got := notifications(t, st)
	if rep.Notified != 1 || len(got) != 1 || episodeOf(got[0]) != 4 {
		t.Fatalf("report = %+v, notifications = %+v; want episode 4 announced", rep, got)
	}
	if rep, _ := w.Poll(ctx); rep.Notified != 0 || len(notifications(t, st)) != 1 {
		t.Fatalf("a later poll announced it again: %+v", rep)
	}
}

// The reported bug: on episode 3 of a show at 9, with part of the backlog recorded by an earlier build,
// every poll announced the rest of it. Only the latest aired episode is news, whatever the records say.
func TestWatcherAnnouncesOnlyTheLatestWhateverWasRecordedBefore(t *testing.T) {
	st := prefetchStore(t)
	setAiring(t, st, 10, time.Now().Add(24*time.Hour))
	ctx := context.Background()
	for ep := 4; ep <= 6; ep++ {
		if err := st.RecordNotified(ctx, fmt.Sprintf("old:%d", ep), 1, ep); err != nil {
			t.Fatal(err)
		}
	}
	var releases []indexer.Torrent
	for ep := 4; ep <= 9; ep++ {
		releases = append(releases, release(fmt.Sprintf("%040d", ep), fmt.Sprintf("[Best] Show - %02d [1080p].mkv", ep), 500))
	}
	w := watcherWith(st, releases...)

	rep, _ := w.Poll(ctx)
	got := notifications(t, st)
	if rep.Notified != 1 || len(got) != 1 || episodeOf(got[0]) != 9 {
		t.Fatalf("report = %+v, notifications = %+v; want episode 9 alone", rep, got)
	}
	if rep, _ := w.Poll(ctx); rep.Notified != 0 || len(notifications(t, st)) != 1 {
		t.Fatalf("a later poll announced more: %+v", rep)
	}
}

// The viewer caught up past what has aired: nothing to say until the next episode airs.
func TestWatcherIsQuietWhenTheViewerIsUpToDate(t *testing.T) {
	st := prefetchStore(t)
	setAiring(t, st, 5, time.Now().Add(time.Hour))
	ctx := context.Background()
	if _, err := st.MarkWatched(ctx, 1, 4); err != nil {
		t.Fatal(err)
	}
	w := watcherWith(st, release(goodHash, "[Best] Show - 04 [1080p].mkv", 500), release(deadHash, "[Best] Show - 05 [1080p].mkv", 500))
	if rep, _ := w.Poll(ctx); rep.Notified != 0 || len(notifications(t, st)) != 0 {
		t.Fatalf("announced an episode already watched or not yet aired: %+v", rep)
	}
}

func finishedShow(t *testing.T, ended time.Time, progress int) *store.Store {
	t.Helper()
	st := prefetchStore(t)
	episodes, current := 12, "CURRENT"
	if _, err := st.ImportList(context.Background(),
		[]store.Anime{{ID: 1, Romaji: "Show", Synonyms: "[]", Genres: "[]", Episodes: &episodes, EndDate: ended.Format("2006-01-02")}},
		[]store.Entry{{ID: 11, AnimeID: 1, Status: &current, Progress: progress}}, store.ImportMerge); err != nil {
		t.Fatal(err)
	}
	return st
}

// A show that finished long ago has nothing new, however far behind the viewer is.
func TestWatcherIsSilentAboutAShowFinishedLongAgo(t *testing.T) {
	st := finishedShow(t, time.Now().AddDate(0, 0, -30), 3)
	ctx := context.Background()
	w := watcherWith(st, release(goodHash, "[Best] Show - 04 [1080p].mkv", 500), release(deadHash, "[Best] Show - 12 [1080p].mkv", 500))
	for range 3 {
		if rep, _ := w.Poll(ctx); rep.Notified != 0 || len(notifications(t, st)) != 0 {
			t.Fatalf("an old episode was announced: %+v", rep)
		}
	}
}

// The finale: the catalogue drops the airing schedule once a show ends, but its last episode is still news.
func TestWatcherAnnouncesTheFinaleOfAShowThatJustEnded(t *testing.T) {
	st := finishedShow(t, time.Now().AddDate(0, 0, -1), 11)
	ctx := context.Background()
	w := watcherWith(st, release(goodHash, "[Best] Show - 12 [1080p].mkv", 500))
	rep, _ := w.Poll(ctx)
	if got := notifications(t, st); rep.Notified != 1 || len(got) != 1 || episodeOf(got[0]) != 12 {
		t.Fatalf("report = %+v, notifications = %+v; want the finale announced", rep, got)
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
