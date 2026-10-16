package library

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"kuro/internal/store"
)

// A finished show played again is a rewatch: only what was watched this time around goes, never the
// episodes ahead that the list's count covers.
func TestAutoDeleteSparesARewatchOfACompletedShow(t *testing.T) {
	c, st := newCache(t)
	ctx := context.Background()
	if err := st.SetSetting(ctx, "cache.autodelete", "now"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.MarkWatched(ctx, 7, 12); err != nil {
		t.Fatal(err)
	}
	if err := st.SetListStatus(ctx, 7, "COMPLETED", -1); err != nil {
		t.Fatal(err)
	}
	for ep := 1; ep <= 3; ep++ {
		episode(t, st, "ep"+string(rune('0'+ep)), ep, 7, ep)
	}
	// Episode 1 finished in the player this time; 2 and 3 are waiting their turn.
	for range 7 {
		if _, err := st.SavePlayback(ctx, store.PlaybackState{AnimeID: 7, EpKey: "1", Position: 1390, Duration: 1400, Played: 120}); err != nil {
			t.Fatal(err)
		}
	}
	watched, _ := st.WatchedEpisodes(ctx, 7)
	if !watched[1] || watched[2] || watched[3] {
		t.Fatalf("watched = %v, want only the episode finished here", watched)
	}

	if n, _ := c.AutoDelete(ctx, 7); n != 1 {
		t.Fatalf("removed %d, want only episode 1", n)
	}
	left := hashes(t, st)
	if left["ep1"] || !left["ep2"] || !left["ep3"] {
		t.Fatalf("left = %v", left)
	}
}

// A pack is as recent as its newest file: one with an episode played today goes after an older single file.
func TestEvictionOrderAgesAPackByItsNewestFile(t *testing.T) {
	old := entry("pack", 4<<30, 10, false, false)
	fresh := entry("pack", 4<<30, 100, false, false)
	single := entry("single", 2<<30, 50, false, false)
	got := evictionOrder([]store.CacheEntry{old, single, fresh}, nil, episodeRef{})
	if len(got) != 3 || got[0].InfoHash != "single" {
		t.Fatalf("order = %+v, want the single file first", got)
	}
}

// Clear reports every file of a pack, not its first.
func TestClearReportsEveryFileOfAPack(t *testing.T) {
	c, st := newCache(t)
	cache(t, st, "batch", 1, 3<<30, false)
	cache(t, st, "batch", 2, 3<<30, false)
	removed, freed, err := c.Clear(context.Background(), false)
	if err != nil || removed != 1 || freed != 6<<30 {
		t.Fatalf("removed %d freed %d err %v", removed, freed, err)
	}
}

// A paused download was never tried, so it does not age toward stalled and evictable.
func TestPausedDownloadNeverCountsAsStalled(t *testing.T) {
	c, st, engine := restartFixture(t, 0)
	ctx := context.Background()
	old := stalledAfter
	stalledAfter = 50 * time.Millisecond
	t.Cleanup(func() { stalledAfter = old })

	engine.ids[1] = "abc"
	engine.paused[1] = true
	record(t, st, "abc", "Frieren - 05", 0)
	for range 3 {
		if err := c.refresh(ctx); err != nil {
			t.Fatal(err)
		}
		time.Sleep(30 * time.Millisecond)
	}
	if c.stalled()["abc"] {
		t.Fatal("a paused download was read as stalled")
	}

	engine.paused[1] = false
	for range 3 {
		if err := c.refresh(ctx); err != nil {
			t.Fatal(err)
		}
		time.Sleep(30 * time.Millisecond)
	}
	if !c.stalled()["abc"] {
		t.Fatal("a download getting nothing should count as stalled")
	}
}

// A kept download the engine stopped listing is still on disk: removing it deletes the file, so the
// bytes the reply counts as freed are freed.
func TestRemovingADownloadTheEngineLostDeletesItsFile(t *testing.T) {
	c, st, _ := restartFixture(t, 0)
	ctx := context.Background()
	record(t, st, "abc", "Frieren - 05.mkv", 0)
	if _, err := st.KeepDownload(ctx, "abc", true); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(c.dir, "Frieren - 05.mkv")
	if err := os.WriteFile(path, make([]byte, 1024), 0o644); err != nil {
		t.Fatal(err)
	}

	freed, err := c.Remove(ctx, "abc")
	if err != nil || freed != 1<<29 {
		t.Fatalf("freed %d err %v", freed, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("the file was left on disk while reported as freed")
	}
	if n := remaining(t, st); n != 0 {
		t.Fatalf("%d rows left", n)
	}
}
