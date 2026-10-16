package library

import (
	"context"
	"errors"
	"testing"
	"time"

	"kuro/internal/indexer"
	"kuro/internal/player"
	"kuro/internal/score"
	"kuro/internal/store"
)

func quickRace(t *testing.T) {
	t.Helper()
	old := raceStagger
	raceStagger = 10 * time.Millisecond
	t.Cleanup(func() { raceStagger = old })
}

// Leaving the page mid-race drops the release that had already warmed; it is nobody's download.
func TestCancelledRaceDiscardsWhatWarmed(t *testing.T) {
	quickRace(t)
	engine := newFakeEngine()
	engine.slow[goodHash] = 5 * time.Second
	p := newPlayback(t, engine, []indexer.Torrent{
		release(goodHash, "[Best] Sousou no Frieren - 01 [1080p BluRay].mkv", 900),
		release(otherHash, "[Worse] Sousou no Frieren - 01 [720p].mkv", 50),
	})

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		eventually(t, "the worse release to warm", func() bool { return torrentsAdded(engine) == 2 })
		time.Sleep(300 * time.Millisecond)
		cancel()
	}()
	if _, _, _, _, _, err := p.attach(ctx, PlayRequest{AnimeID: 1, Episode: 1, Season: 1, Prefs: score.DefaultPreferences()}); !errors.Is(err, context.Canceled) {
		t.Fatalf("attach = %v, want the cancel", err)
	}
	eventually(t, "the warmed release to be discarded", func() bool { return engine.wasDeleted(otherHash) })
}

// A candidate that fails at once hands its turn to the next without the stagger.
func TestAFailedCandidateDoesNotHoldTheNextBack(t *testing.T) {
	old := raceStagger
	raceStagger = 3 * time.Second
	t.Cleanup(func() { raceStagger = old })
	engine := newFakeEngine(goodHash)
	p := newPlayback(t, engine, []indexer.Torrent{
		release(goodHash, "[Dead] Sousou no Frieren - 01 [1080p].mkv", 900),
		release(otherHash, "[Live] Sousou no Frieren - 01 [1080p].mkv", 50),
	})

	start := time.Now()
	rel, _, _, _, _, err := p.attach(context.Background(), PlayRequest{AnimeID: 1, Episode: 1, Season: 1, Prefs: score.DefaultPreferences()})
	if err != nil || rel.Torrent.InfoHash != otherHash {
		t.Fatalf("attach = %s, %v", rel.Torrent.InfoHash, err)
	}
	if took := time.Since(start); took >= raceStagger {
		t.Fatalf("took %v: the dead candidate held the live one back for the whole stagger", took)
	}
}

// mpv closing unpins the episode, pauses its download per the prefs, and resumes what yielded.
func TestPlayerExitReleasesTheEpisode(t *testing.T) {
	engine := newFakeEngine()
	engine.ids[1] = goodHash
	engine.ids[2] = otherHash
	p := newPlayback(t, engine, nil)
	ctx := context.Background()
	if err := p.store.SetSetting(ctx, "cache.prefetch_full", "false"); err != nil {
		t.Fatal(err)
	}
	if err := p.store.RecordTorrent(ctx, store.TorrentRecord{
		InfoHash: goodHash, EngineID: 1, Name: "[Group] Sousou no Frieren - 05 [1080p].mkv", AnimeID: 1, EpKey: "5", FilePath: "x.mkv", TotalSize: 100,
	}); err != nil {
		t.Fatal(err)
	}
	if err := p.store.PinOnly(ctx, goodHash, 0); err != nil {
		t.Fatal(err)
	}
	p.yieldMu.Lock()
	p.yielded = []string{otherHash}
	p.yieldMu.Unlock()
	engine.paused[2] = true

	pl := &fakePlayer{events: make(chan player.Event, 2)}
	pl.events <- player.Event{Kind: player.EventPosition, Position: 10, Duration: 1400}
	pl.events <- player.Event{Kind: player.EventExit}
	close(pl.events)
	p.track(pl, 1, 5, 1, p.trackGen.Load())

	entries, _ := p.store.CacheEntries(ctx)
	for _, e := range entries {
		if e.Pinned {
			t.Fatal("the episode stayed pinned after the player closed")
		}
	}
	if !engine.isPaused(1) {
		t.Error("with keep-whole-episode off, the download kept running after the player closed")
	}
	if engine.isPaused(2) {
		t.Error("the download that yielded to the episode was not resumed")
	}
}

// Relaunching ends the previous mpv; its tracker must not unpin the episode the new one is playing.
func TestStaleTrackerDoesNotUnpin(t *testing.T) {
	engine := newFakeEngine()
	engine.ids[1] = goodHash
	p := newPlayback(t, engine, nil)
	ctx := context.Background()
	if err := p.store.RecordTorrent(ctx, store.TorrentRecord{
		InfoHash: goodHash, EngineID: 1, Name: "x", AnimeID: 1, EpKey: "5", FilePath: "x.mkv", TotalSize: 100,
	}); err != nil {
		t.Fatal(err)
	}
	if err := p.store.PinOnly(ctx, goodHash, 0); err != nil {
		t.Fatal(err)
	}

	old := &fakePlayer{events: make(chan player.Event, 1)}
	gen := p.trackGen.Load()
	done := make(chan struct{})
	go func() { p.track(old, 1, 5, 1, gen); close(done) }()
	p.trackGen.Add(1) // the relaunch, before the old player is told to stop
	old.events <- player.Event{Kind: player.EventExit}
	close(old.events)
	<-done

	entries, _ := p.store.CacheEntries(ctx)
	if len(entries) != 1 || !entries[0].Pinned {
		t.Fatalf("entries = %+v, want the episode still pinned", entries)
	}
}

// A release picked by hand leaves the resolved one for the next ordinary play.
func TestHandPickLeavesThePreparedReleaseAlone(t *testing.T) {
	quickRace(t)
	engine := newFakeEngine()
	p := newPlayback(t, engine, []indexer.Torrent{
		release(goodHash, "[Best] Sousou no Frieren - 01 [1080p].mkv", 900),
		release(otherHash, "[Other] Sousou no Frieren - 01 [1080p].mkv", 50),
	})
	pref := NewPrefetcher(p.store, p.finder, p.torrent, discard())
	p.WithPrefetcher(pref)
	resolved := score.Rank([]score.Candidate{{Torrent: release(goodHash, "[Best] Sousou no Frieren - 01 [1080p].mkv", 900), Confirmed: true}}, score.DefaultPreferences())[0]
	pref.storePrepared(1, 1, resolved)

	if _, _, _, _, _, err := p.attach(context.Background(), PlayRequest{AnimeID: 1, Episode: 1, Season: 1, Prefs: score.DefaultPreferences(), InfoHash: otherHash}); err != nil {
		t.Fatal(err)
	}
	if _, ok := pref.TakePrepared(1, 1); !ok {
		t.Fatal("the hand pick consumed the release resolved for the ordinary play")
	}
}

// A stream opening between the queue's look and its start stands the download down at once.
func TestQueueStandsDownWhenAStreamOpensAsItStarts(t *testing.T) {
	d, st, _ := workerQueueWith(t, release(otherHash, "[Group] Show - 02 [1080p].mkv", 50))
	ctx := context.Background()
	if _, err := st.Enqueue(ctx, 1, 1, []int{2}); err != nil {
		t.Fatal(err)
	}
	// Watching already when step runs: Run would not have called it, but a stream can open in between.
	d.mu.Lock()
	d.watching["1-9"] = struct{}{}
	d.mu.Unlock()

	if !d.step(ctx) {
		t.Fatal("step did nothing")
	}
	rows, _ := st.QueuedDownloads(ctx)
	if len(rows) != 1 || rows[0].State != "pending" {
		t.Fatalf("rows = %+v, want the episode back in line", rows)
	}
}

// A dub show's new episode is downloaded although the global "notify for" filter asks for subs: that
// filter is for the notification, the download follows the show's own prefs.
func TestAutoDownloadFollowsTheShowNotTheNotifyFilter(t *testing.T) {
	st := airingStore(t, 6, time.Now().Add(-time.Hour).Unix(), 5)
	ctx := context.Background()
	if err := st.SetAnimePref(ctx, 7, "audio.prefer", "dub"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetAnimePref(ctx, 7, "autodownload.enabled", "true"); err != nil {
		t.Fatal(err)
	}
	source := fixedIndexer{results: []indexer.Torrent{
		release("1111111111111111111111111111111111111111", "[Group] Bleach - Sennen Kessen-hen - Kashin Tan - 06 [1080p][English Dub].mkv", 40),
	}}
	w := NewWatcher(st, NewFinder(st, source, discard()), source, discard()).
		WithDownloader(NewDownloader(st, NewPrefetcher(st, nil, nil, discard()), nil, discard()))
	rep, err := w.Poll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Found != 1 || rep.Downloaded != 1 {
		t.Fatalf("report = %+v, want the dub episode queued", rep)
	}
	if rep.Notified != 0 {
		t.Errorf("notified %d times about a release the notify filter does not cover", rep.Notified)
	}
}
