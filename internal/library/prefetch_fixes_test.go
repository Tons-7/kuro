package library

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"kuro/internal/score"
	"kuro/internal/store"
)

// A release chosen by hand is recorded as such, so the identity check cannot replace it on the next play.
func TestHandPickedReleaseIsRecordedAsManual(t *testing.T) {
	engine := newFakeEngine()
	engine.finished[ep2Also] = true
	play, pref, _ := pipeline(t, engine, frieren(ep2Hash, 2), release(ep2Also, "[CBM] Monster - 02 - Downfall [1080p].mkv", 50))
	ctx := context.Background()

	if err := pref.DownloadRelease(ctx, 1, 2, 1, score.DefaultPreferences(), time.Second, ep2Also); err != nil {
		t.Fatalf("download: %v", err)
	}
	rec, ok, _ := play.store.TorrentForEpisode(ctx, 1, "2")
	if !ok || rec.InfoHash != ep2Also || !rec.Manual {
		t.Fatalf("recorded %+v, want the chosen release marked manual", rec)
	}
	if _, held := pref.held(ctx, 1, 2); !held {
		t.Fatal("the chosen release is not held for the episode")
	}
}

// A fetch's claim on its torrent ends with the fetch, so adoption resumes for one left unrecorded.
func TestClaimEndsWithTheFetch(t *testing.T) {
	_, pref, _ := pipeline(t, newFakeEngine())
	flight, _ := pref.beginFetch(1, 2)
	pref.claim(flight, "ABC")
	if !pref.Claimed("abc") {
		t.Fatal("not claimed")
	}
	pref.endFetch(1, 2, flight)
	if pref.Claimed("abc") {
		t.Fatal("still claimed after the fetch ended")
	}
}

// A release resolved a moment ago is not resolved again on the next visit.
func TestPrepareDoesNotRepeatAFreshResolve(t *testing.T) {
	_, pref, idx := pipeline(t, newFakeEngine(), frieren(ep2Hash, 2))
	prefs := score.DefaultPreferences()
	pref.PrepareTarget(1, 2, 1, prefs)
	eventually(t, "the resolve", func() bool {
		pref.mu.Lock()
		defer pref.mu.Unlock()
		_, ok := pref.prepared[prepareKey(1, 2)]
		return ok
	})
	searched := idx.searches()

	pref.PrepareTarget(1, 2, 1, prefs)
	time.Sleep(100 * time.Millisecond)
	if n := idx.searches(); n != searched {
		t.Fatalf("searched %d more times for a release resolved a moment ago", n-searched)
	}

	// An old one is worth resolving again.
	pref.mu.Lock()
	got := pref.prepared[prepareKey(1, 2)]
	got.at = time.Now().Add(-preparedFresh - time.Minute)
	pref.prepared[prepareKey(1, 2)] = got
	pref.mu.Unlock()
	pref.PrepareTarget(1, 2, 1, prefs)
	eventually(t, "a second resolve", func() bool { return idx.searches() > searched })
}

// A library row whose file was deleted outside kuro no longer stands in for the episode: the queue
// downloads it, and the row is marked missing.
func TestAStaleLibraryRowDoesNotStandInForTheEpisode(t *testing.T) {
	engine := newFakeEngine()
	engine.finished[ep2Hash] = true
	play, pref, _ := pipeline(t, engine, frieren(ep2Hash, 2))
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "Frieren - 02.mkv")
	if _, err := play.store.SaveLocalFiles(ctx, time.Now().Unix(), []store.LocalFile{{
		Path: path, Size: 1, AnimeID: 1, Episode: 2, Confidence: 1,
	}}); err != nil {
		t.Fatal(err)
	}

	if err := pref.Download(ctx, 1, 2, 1, score.DefaultPreferences(), time.Second); err != nil {
		t.Fatalf("download: %v", err)
	}
	if n := torrentsAdded(engine); n != 1 {
		t.Fatalf("added %d torrents, want the download of the episode whose file is gone", n)
	}
	if f, _ := play.store.LocalEpisode(ctx, 1, 2); f.ID != 0 {
		t.Fatal("the stale row still stands")
	}

	// With the file there, the library copy is used and nothing is downloaded.
	third := filepath.Join(filepath.Dir(path), "Frieren - 03.mkv")
	if err := os.WriteFile(third, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := play.store.SaveLocalFiles(ctx, time.Now().Unix(), []store.LocalFile{{
		Path: third, Size: 1, AnimeID: 1, Episode: 3, Confidence: 1,
	}}); err != nil {
		t.Fatal(err)
	}
	if err := pref.Download(ctx, 1, 3, 1, score.DefaultPreferences(), time.Second); err != nil {
		t.Fatalf("download: %v", err)
	}
	if n := torrentsAdded(engine); n != 1 {
		t.Fatalf("added %d torrents; a file on disk needs no download", n)
	}
}

// Giving up on a download whose episode is playing leaves it running: pausing it would freeze the stream.
func TestStoppingADownloadSparesTheOnePlaying(t *testing.T) {
	engine := newFakeEngine()
	engine.stuck[ep2Hash] = true
	play, pref, _ := pipeline(t, engine, frieren(ep2Hash, 2))
	ctx := context.Background()

	old := prefetchWarmCap
	prefetchWarmCap = 300 * time.Millisecond
	t.Cleanup(func() { prefetchWarmCap = old })

	outcome := fetchInBackground(pref, false)
	eventually(t, "the fetch to add its torrent", func() bool { _, ok := pref.Fetching(1, 2); return ok })
	// Play takes it over and the stream pins it.
	if err := play.store.RecordTorrent(ctx, store.TorrentRecord{
		InfoHash: ep2Hash, EngineID: 1, Name: "[Best] Sousou no Frieren - 02 [1080p].mkv", AnimeID: 1, EpKey: "2",
		FilePath: "x.mkv", TotalSize: 100,
	}); err != nil {
		t.Fatal(err)
	}
	if err := play.store.PinOnly(ctx, ep2Hash, 0); err != nil {
		t.Fatal(err)
	}

	if _, started, err := outcome(); started || !errors.Is(err, errOvertaken) {
		t.Fatalf("fetch: started=%v err=%v, want it to yield to play", started, err)
	}
	if engine.isPausedHash(ep2Hash) {
		t.Fatal("the fetch paused the torrent play is streaming")
	}
	if engine.wasDeleted(ep2Hash) {
		t.Fatal("the fetch deleted the torrent play is streaming")
	}
}
