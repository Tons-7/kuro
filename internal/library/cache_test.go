package library

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"kuro/internal/db"
	"kuro/internal/store"
)

func entry(hash string, bytes int64, lastPlayed int64, pinned, protected bool) store.CacheEntry {
	return store.CacheEntry{
		InfoHash: hash, Bytes: bytes, LastPlayed: lastPlayed,
		Pinned: pinned, Protected: protected,
	}
}

// One of the pinned files is playing right now, so it can never be a
// candidate no matter how the budget looks.
func TestEvictionOrderNeverOffersPinned(t *testing.T) {
	got := evictionOrder([]store.CacheEntry{
		entry("pinned", 1<<30, 100, true, false),
		entry("old", 1<<30, 50, false, false),
	})

	if len(got) != 1 || got[0].InfoHash != "old" {
		t.Fatalf("order = %+v", got)
	}
}

func TestEvictionOrderIsOldestFirst(t *testing.T) {
	got := evictionOrder([]store.CacheEntry{
		entry("newest", 1<<30, 300, false, false),
		entry("oldest", 1<<30, 100, false, false),
		entry("middle", 1<<30, 200, false, false),
	})

	want := []string{"oldest", "middle", "newest"}
	for i, w := range want {
		if got[i].InfoHash != w {
			t.Fatalf("position %d = %q, want %q", i, got[i].InfoHash, w)
		}
	}
}

// Evicting an episode of a season the user is midway through is the worst
// available choice, so those go last — but they still go if the budget demands.
func TestProtectedEntriesAreSacrificedLast(t *testing.T) {
	got := evictionOrder([]store.CacheEntry{
		entry("watching-old", 1<<30, 10, false, true),
		entry("finished-new", 1<<30, 900, false, false),
	})

	if len(got) != 2 {
		t.Fatalf("got %d candidates", len(got))
	}
	if got[0].InfoHash != "finished-new" {
		t.Fatalf("first candidate = %q; an in-progress show should outlive a finished one", got[0].InfoHash)
	}
	if got[1].InfoHash != "watching-old" {
		t.Fatalf("protected entry missing from the order: %+v", got)
	}
}

func TestEvictionOrderEmptyAndAllPinned(t *testing.T) {
	if got := evictionOrder(nil); len(got) != 0 {
		t.Fatalf("got %d candidates from nothing", len(got))
	}

	allPinned := []store.CacheEntry{
		entry("a", 1<<30, 1, true, false),
		entry("b", 1<<30, 2, true, true),
	}
	if got := evictionOrder(allPinned); len(got) != 0 {
		t.Fatalf("got %d candidates when everything is pinned", len(got))
	}
}

// Eviction deletes the torrent, not the file, so an idle episode sharing a
// batch with the one playing would take it down mid-scene.
func TestPinCoversEverySiblingOfTheSameTorrent(t *testing.T) {
	playing := entry("batch", 1<<30, 900, true, false)
	playing.FileIndex = 3
	idle := entry("batch", 1<<30, 10, false, false)
	idle.FileIndex = 7

	if got := evictionOrder([]store.CacheEntry{playing, idle}); len(got) != 0 {
		t.Fatalf("offered %+v from a torrent with a file playing", got)
	}
}

func newCache(t *testing.T) (*Cache, *store.Store) {
	t.Helper()

	conn, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := conn.Migrate(); err != nil {
		t.Fatal(err)
	}

	st := store.New(conn)
	return NewCache(st, nil, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil))), st
}

func cache(t *testing.T, s *store.Store, hash string, index int, bytes int64, pinned bool) {
	t.Helper()

	ctx := context.Background()
	if err := s.RecordTorrent(ctx, store.TorrentRecord{
		InfoHash: hash, RqbitID: index, Name: hash,
		FileIndex: index, FilePath: hash, TotalSize: bytes,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCacheBytes(ctx, hash, index, bytes, true); err != nil {
		t.Fatal(err)
	}
	// Recording pins unconditionally, so unpinning is what makes an entry a
	// candidate at all.
	if err := s.PinCache(ctx, hash, index, pinned); err != nil {
		t.Fatal(err)
	}
}

// Two files of one torrent are one deletion; freed bytes must be counted once.
func TestSweepCountsATorrentOnce(t *testing.T) {
	c, st := newCache(t)
	ctx := context.Background()

	cache(t, st, "batch", 1, 3<<30, false)
	cache(t, st, "batch", 2, 3<<30, false)
	cache(t, st, "keep", 3, 2<<30, true)
	if err := st.SetSetting(ctx, "cache.budget_bytes", "5368709120"); err != nil {
		t.Fatal(err)
	}

	rep, err := c.Sweep(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if rep.Evicted != 1 {
		t.Fatalf("evicted %d torrents; the batch is a single deletion", rep.Evicted)
	}
	if rep.Freed != 6<<30 {
		t.Fatalf("freed %d bytes, want %d: both files of the batch are gone", rep.Freed, int64(6)<<30)
	}
	if rep.After > rep.Budget {
		t.Fatalf("finished at %d over a budget of %d", rep.After, rep.Budget)
	}

	left, err := st.CacheEntries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 1 || left[0].InfoHash != "keep" {
		t.Fatalf("remaining entries = %+v, want only keep", left)
	}
}

func TestEvictionOrderNeverOffersKept(t *testing.T) {
	kept := entry("kept", 1<<30, 10, false, false)
	kept.Kept = true

	got := evictionOrder([]store.CacheEntry{kept, entry("cached", 1<<30, 500, false, false)})
	if len(got) != 1 || got[0].InfoHash != "cached" {
		t.Fatalf("order = %+v, want only the cached entry", got)
	}
}

// A kept download is outside the budget: it neither fills the cache nor is taken
// to make room, however large it is.
func TestSweepNeverEvictsKeptDownloads(t *testing.T) {
	c, st := newCache(t)
	ctx := context.Background()

	cache(t, st, "kept", 1, 6<<30, false)
	cache(t, st, "cached", 2, 3<<30, false)
	if _, err := st.KeepDownload(ctx, "kept", true); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(ctx, "cache.budget_bytes", "5368709120"); err != nil {
		t.Fatal(err)
	}

	rep, err := c.Sweep(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Before != 3<<30 {
		t.Errorf("usage = %d, want the cached 3 GiB only", rep.Before)
	}
	if rep.Evicted != 0 {
		t.Fatalf("evicted %d with the cache under budget", rep.Evicted)
	}

	// Now the cache itself is over: the oldest cached file goes, the kept one stays.
	cache(t, st, "cached-too", 3, 3<<30, false)
	rep, err = c.Sweep(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Evicted != 1 || rep.After > rep.Budget {
		t.Fatalf("evicted %d, finished at %d of %d", rep.Evicted, rep.After, rep.Budget)
	}
	left, _ := st.CacheEntries(ctx)
	var keptLeft bool
	for _, e := range left {
		keptLeft = keptLeft || e.InfoHash == "kept"
	}
	if !keptLeft {
		t.Fatalf("the kept download was evicted: %+v", left)
	}
}

func episode(t *testing.T, s *store.Store, hash string, index, animeID, ep int) {
	t.Helper()
	ctx := context.Background()
	if err := s.RecordTorrent(ctx, store.TorrentRecord{
		InfoHash: hash, RqbitID: index, Name: hash, AnimeID: animeID, EpKey: fmt.Sprint(ep),
		FileIndex: index, FilePath: hash, TotalSize: 1 << 30,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCacheBytes(ctx, hash, index, 1<<30, true); err != nil {
		t.Fatal(err)
	}
}

func hashes(t *testing.T, s *store.Store) map[string]bool {
	t.Helper()
	entries, err := s.CacheEntries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, e := range entries {
		out[e.InfoHash] = true
	}
	return out
}

func TestAutoDeleteIsOffByDefault(t *testing.T) {
	c, st := newCache(t)
	ctx := context.Background()
	episode(t, st, "ep1", 1, 7, 1)
	if _, err := st.MarkWatched(ctx, 7, 1); err != nil {
		t.Fatal(err)
	}

	if n, err := c.AutoDelete(ctx, 7); err != nil || n != 0 {
		t.Fatalf("removed %d (err %v) with auto-delete off", n, err)
	}
}

// "now": a watched episode goes once nothing plays it; unwatched, pinned and
// kept ones stay.
func TestAutoDeleteNowTakesOnlyWatchedCachedEpisodes(t *testing.T) {
	c, st := newCache(t)
	ctx := context.Background()
	if err := st.SetSetting(ctx, "cache.autodelete", "now"); err != nil {
		t.Fatal(err)
	}

	episode(t, st, "seen", 1, 7, 1)
	episode(t, st, "playing", 2, 7, 2)
	episode(t, st, "kept", 3, 7, 3)
	episode(t, st, "unseen", 4, 7, 4)
	if _, err := st.MarkWatched(ctx, 7, 3); err != nil {
		t.Fatal(err)
	}
	if err := st.PinOnly(ctx, "playing", 2); err != nil {
		t.Fatal(err)
	}
	if _, err := st.KeepDownload(ctx, "kept", true); err != nil {
		t.Fatal(err)
	}

	if n, err := c.AutoDelete(ctx, 7); err != nil || n != 1 {
		t.Fatalf("removed %d (err %v), want just the watched cached one", n, err)
	}
	left := hashes(t, st)
	if left["seen"] || !left["playing"] || !left["kept"] || !left["unseen"] {
		t.Fatalf("left = %v", left)
	}

	// Opting downloads in takes the kept one too.
	if err := st.SetSetting(ctx, "cache.autodelete_downloads", "true"); err != nil {
		t.Fatal(err)
	}
	if n, _ := c.AutoDelete(ctx, 7); n != 1 {
		t.Fatalf("removed %d, want the kept one", n)
	}
	if left := hashes(t, st); left["kept"] || !left["playing"] {
		t.Fatalf("left = %v", left)
	}
}

// "keep2": an episode goes only once the two after it are watched, so the one
// before the current stays for going back.
func TestAutoDeleteKeep2WaitsForTwoMore(t *testing.T) {
	c, st := newCache(t)
	ctx := context.Background()
	if err := st.SetSetting(ctx, "cache.autodelete", "keep2"); err != nil {
		t.Fatal(err)
	}
	for ep := 1; ep <= 4; ep++ {
		episode(t, st, fmt.Sprintf("ep%d", ep), ep, 7, ep)
	}

	if _, err := st.MarkWatched(ctx, 7, 2); err != nil {
		t.Fatal(err)
	}
	if n, _ := c.AutoDelete(ctx, 7); n != 0 {
		t.Fatalf("removed %d after two watched; episode 1 needs 2 and 3", n)
	}

	if _, err := st.MarkWatched(ctx, 7, 3); err != nil {
		t.Fatal(err)
	}
	if n, _ := c.AutoDelete(ctx, 7); n != 1 {
		t.Fatalf("removed %d after three watched, want episode 1", n)
	}
	left := hashes(t, st)
	if left["ep1"] || !left["ep2"] || !left["ep3"] || !left["ep4"] {
		t.Fatalf("left = %v", left)
	}
}

// Deleting a torrent takes every file in it, so a batch stays while any
// episode of it is still wanted.
func TestAutoDeleteSparesABatchWithAnUnwatchedEpisode(t *testing.T) {
	c, st := newCache(t)
	ctx := context.Background()
	if err := st.SetSetting(ctx, "cache.autodelete", "now"); err != nil {
		t.Fatal(err)
	}
	episode(t, st, "batch", 1, 7, 1)
	episode(t, st, "batch", 2, 7, 2)
	if _, err := st.MarkWatched(ctx, 7, 1); err != nil {
		t.Fatal(err)
	}

	if n, _ := c.AutoDelete(ctx, 7); n != 0 {
		t.Fatalf("removed %d; episode 2 of the batch is unwatched", n)
	}
	if _, err := st.MarkWatched(ctx, 7, 2); err != nil {
		t.Fatal(err)
	}
	if n, _ := c.AutoDelete(ctx, 7); n != 1 {
		t.Fatalf("removed %d once both are watched", n)
	}
}
