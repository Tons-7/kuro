package library

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"kuro/internal/indexer"
	"kuro/internal/score"
	"kuro/internal/store"
	"kuro/internal/torrent"
)

// workerQueue is a queue over a fake engine holding episode 1 complete, with
// nothing to find for any other episode.
func workerQueue(t *testing.T) (*Downloader, *store.Store) {
	t.Helper()
	d, st, _ := workerQueueWith(t)
	return d, st
}

// workerQueueWith is workerQueue with an indexer offering these releases, and the engine to look at.
func workerQueueWith(t *testing.T, releases ...indexer.Torrent) (*Downloader, *store.Store, *fakeEngine) {
	t.Helper()
	engine := newFakeEngine()
	srv := httptest.NewServer(engine.handler())
	t.Cleanup(srv.Close)

	st := prefetchStore(t)
	ctx := context.Background()
	episodes := 12
	if _, err := st.ImportList(ctx,
		[]store.Anime{{ID: 1, Romaji: "Show", Synonyms: "[]", Genres: "[]", Episodes: &episodes}},
		nil, store.ImportMerge); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordTorrent(ctx, store.TorrentRecord{
		// Named for the show: a held copy that does not name it is not trusted.
		InfoHash: goodHash, Name: "[Group] Show - 01 [1080p].mkv", AnimeID: 1, EpKey: "1", FilePath: "Show - 01.mkv", TotalSize: 100,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetCacheBytes(ctx, goodHash, 0, 100, true); err != nil {
		t.Fatal(err)
	}
	// An id the engine will not hand out itself (it counts from 1).
	engine.ids[99] = goodHash
	engine.finished[goodHash] = true

	finder := NewFinder(st, fixedIndexer{results: releases}, discard())
	prefs := func(context.Context, int) score.Preferences { return score.DefaultPreferences() }
	return NewDownloader(st, NewPrefetcher(st, finder, torrent.NewClient(srv.URL), discard()), prefs, discard()), st, engine
}

// A release chosen by hand replaces the copy held under another: that one is fetched, the old one goes.
func TestWorkerDownloadsTheReleaseChosenOverTheHeldOne(t *testing.T) {
	d, st, engine := workerQueueWith(t,
		release(goodHash, "[Group] Show - 01 [1080p].mkv", 500),
		release(otherHash, "[Other] Show - 01 [720p].mkv", 3),
	)
	engine.finished[otherHash] = true
	ctx := context.Background()
	if _, err := st.Enqueue(ctx, 1, 1, []int{1}); err != nil {
		t.Fatal(err)
	}
	if set, err := st.SetQueuedRelease(ctx, 1, "1", otherHash, "[Other] Show - 01 [720p].mkv"); err != nil || !set {
		t.Fatalf("set=%v err=%v", set, err)
	}

	runUntil(t, d, func() bool { return len(queued(t, st)) == 0 })

	if rec, ok, _ := st.TorrentForEpisode(ctx, 1, "1"); !ok || rec.InfoHash != otherHash {
		t.Fatalf("episode 1 is %+v, want the chosen release", rec)
	}
	if !engine.wasDeleted(goodHash) {
		t.Error("the copy held before is still in the engine")
	}
	entries, _ := st.CacheEntries(ctx)
	if len(entries) != 1 || entries[0].InfoHash != otherHash || !entries[0].Kept {
		t.Errorf("entries = %+v, want the chosen copy alone, kept", entries)
	}
}

// A chosen release the sites no longer list fails the row rather than downloading something else.
func TestWorkerFailsWhenTheChosenReleaseIsGone(t *testing.T) {
	d, st, _ := workerQueueWith(t, release(goodHash, "[Group] Show - 02 [1080p].mkv", 500))
	ctx := context.Background()
	if _, err := st.Enqueue(ctx, 1, 1, []int{2}); err != nil {
		t.Fatal(err)
	}
	st.SetQueuedRelease(ctx, 1, "2", otherHash, "[Gone] Show - 02")

	runUntil(t, d, func() bool {
		items := queued(t, st)
		return len(items) == 1 && items[0].State == "failed"
	})
	if failed := queued(t, st)[0]; failed.Error == nil || !strings.Contains(*failed.Error, "not found") {
		t.Errorf("failed row = %+v, want it to say the release was not found", failed)
	}
}

// Restart puts the episode first and stops the one downloading, even when that is the episode itself.
func TestRestartStopsTheEpisodeDownloading(t *testing.T) {
	d, st := workerQueue(t)
	ctx := context.Background()
	if _, err := st.Enqueue(ctx, 1, 1, []int{1, 2}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.NextQueued(ctx); err != nil {
		t.Fatal(err)
	}
	running, stop := context.WithCancel(ctx)
	d.mu.Lock()
	d.current = &inFlight{animeID: 1, epKey: "1", stop: stop}
	d.mu.Unlock()

	if moved, err := d.Restart(ctx, 1, "1"); err != nil || !moved {
		t.Fatalf("moved=%v err=%v", moved, err)
	}
	if running.Err() == nil {
		t.Error("the download in flight was not stopped")
	}
}

func queued(t *testing.T, st *store.Store) []store.QueuedDownload {
	t.Helper()
	items, err := st.QueuedDownloads(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return items
}

// runUntil runs the worker until done reports true, failing after five seconds.
func runUntil(t *testing.T, d *Downloader, done func() bool) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() { d.Run(ctx); close(stopped) }()
	t.Cleanup(func() { cancel(); <-stopped })

	for deadline := time.Now().Add(5 * time.Second); !done(); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the worker never got there")
		}
	}
}

func TestWorkerTakesAFinishedEpisodeOffTheQueue(t *testing.T) {
	d, st := workerQueue(t)
	ctx := context.Background()
	if _, err := st.Enqueue(ctx, 1, 1, []int{1}); err != nil {
		t.Fatal(err)
	}

	runUntil(t, d, func() bool { return len(queued(t, st)) == 0 })

	entries, _ := st.CacheEntries(ctx)
	if len(entries) != 1 || !entries[0].Kept {
		t.Errorf("entries = %+v, want the downloaded episode kept", entries)
	}
}

// A release that cannot be found leaves a failed row saying why, and the worker moves on.
func TestWorkerMarksAnEpisodeItCannotFindAsFailed(t *testing.T) {
	d, st := workerQueue(t)
	ctx := context.Background()
	if _, err := st.Enqueue(ctx, 1, 1, []int{5}); err != nil {
		t.Fatal(err)
	}

	runUntil(t, d, func() bool {
		items := queued(t, st)
		return len(items) == 1 && items[0].State == "failed"
	})
	if failed := queued(t, st)[0]; failed.Error == nil || *failed.Error == "" {
		t.Error("the failed row does not say why")
	}
}

// Whatever a crash left marked active goes back in line, and a paused queue stays put.
func TestWorkerRequeuesInterruptedWorkAndRespectsThePause(t *testing.T) {
	d, st := workerQueue(t)
	ctx := context.Background()
	if _, err := st.Enqueue(ctx, 1, 1, []int{1}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := st.NextQueued(ctx); err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	if err := d.SetPaused(ctx, true); err != nil {
		t.Fatal(err)
	}

	runUntil(t, d, func() bool {
		items := queued(t, st)
		return len(items) == 1 && items[0].State == "pending"
	})
	time.Sleep(200 * time.Millisecond)
	if items := queued(t, st); len(items) != 1 || items[0].State != "pending" {
		t.Fatalf("a paused queue worked anyway: %+v", items)
	}

	if err := d.SetPaused(ctx, false); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); len(queued(t, st)) != 0; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("resuming did not start the waiting episode")
		}
	}
}

// Watching gives the connection back: another episode's download stops, the watched one's does not.
func TestHoldStopsADownloadUnlessItIsTheEpisodeBeingWatched(t *testing.T) {
	d, _ := pausableQueue(t)
	stopped := false
	d.current = &inFlight{animeID: 1, epKey: "3", stop: func() { stopped = true }}

	d.Hold("1-3")
	if stopped {
		t.Fatal("the download feeding the episode on screen was stopped")
	}
	if !d.Downloading("1-3") || d.Downloading("1-4") {
		t.Error("Downloading does not name the episode in flight")
	}
	if !d.held() {
		t.Error("the queue would start another download while an episode plays")
	}

	d.Hold("1-4")
	if !stopped {
		t.Error("a download kept the connection while a different episode plays")
	}
}

func TestReleaseGivesTheQueueBackOnlyOnceNothingIsWatching(t *testing.T) {
	d, _ := pausableQueue(t)
	d.Hold("1-1")
	d.Hold("1-1")
	d.Hold("2-1")

	d.Release("1-1")
	if !d.held() {
		t.Fatal("the queue resumed while another session is still watching")
	}
	select {
	case <-d.wake:
		t.Fatal("the worker was woken with a session still watching")
	default:
	}

	d.Release("2-1")
	if d.held() {
		t.Fatal("the queue is still held with nothing watching")
	}
	select {
	case <-d.wake:
	default:
		t.Error("the worker was not woken once nothing was watching")
	}
}

// Cancelling reaches the episode in flight, which has no queue row left to delete.
func TestCancelStopsTheEpisodeInFlight(t *testing.T) {
	d, st := pausableQueue(t)
	ctx := context.Background()
	if _, err := st.Enqueue(ctx, 1, 1, []int{4, 5}); err != nil {
		t.Fatal(err)
	}
	stopped := false
	d.current = &inFlight{animeID: 1, epKey: "3", stop: func() { stopped = true }}

	if n, err := d.Cancel(ctx, 1, "4"); err != nil || n != 1 || stopped {
		t.Fatalf("cancelling a waiting episode: removed=%d stopped=%v err=%v", n, stopped, err)
	}
	if n, err := d.Cancel(ctx, 2, ""); err != nil || n != 0 || stopped {
		t.Fatalf("cancelling another show: removed=%d stopped=%v err=%v", n, stopped, err)
	}
	if n, err := d.Cancel(ctx, 1, ""); err != nil || n != 2 || !stopped {
		t.Fatalf("cancelling the show: removed=%d stopped=%v err=%v, want the waiting one and the one in flight", n, stopped, err)
	}
}
