package store

import (
	"context"
	"testing"
)

// A download someone asked for is not cache: it costs the budget nothing, and
// the downloads list says which tier a row is in.
func TestCacheUsageLeavesKeptDownloadsOutOfTheBudget(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	for _, hash := range []string{"cached", "kept"} {
		if err := s.RecordTorrent(ctx, TorrentRecord{
			InfoHash: hash, RqbitID: 1, Name: hash, FilePath: hash + ".mkv", TotalSize: 100,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetCacheBytes(ctx, "cached", 0, 3<<30, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCacheBytes(ctx, "kept", 0, 4<<30, true); err != nil {
		t.Fatal(err)
	}

	found, err := s.KeepDownload(ctx, "kept", true)
	if err != nil || !found {
		t.Fatalf("keep: found=%v err=%v", found, err)
	}
	if found, _ := s.KeepDownload(ctx, "nope", true); found {
		t.Error("kept a download that does not exist")
	}

	usage, err := s.CacheUsage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if usage.Bytes != 3<<30 {
		t.Errorf("budget sees %d bytes, want only the cached 3 GiB", usage.Bytes)
	}
	if usage.Kept != 1 || usage.KeptBytes != 4<<30 {
		t.Errorf("kept = %d / %d bytes, want 1 / 4 GiB", usage.Kept, usage.KeptBytes)
	}

	items, err := s.DownloadStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	kept := map[string]bool{}
	for _, d := range items {
		kept[d.InfoHash] = d.Kept
	}
	if !kept["kept"] || kept["cached"] {
		t.Errorf("download rows report kept = %v", kept)
	}

	// Handing it back to the cache counts it again.
	if _, err := s.KeepDownload(ctx, "kept", false); err != nil {
		t.Fatal(err)
	}
	if usage, _ := s.CacheUsage(ctx); usage.Bytes != 7<<30 || usage.Kept != 0 {
		t.Errorf("after unkeep: %d bytes, %d kept", usage.Bytes, usage.Kept)
	}
}

// A kept episode fetched again under another release stays kept, and the old
// release is reported so the engine can drop it.
func TestRecordTorrentCarriesKeepToTheReplacement(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	record := func(hash string, index int, ep string) {
		t.Helper()
		if err := s.RecordTorrent(ctx, TorrentRecord{
			InfoHash: hash, RqbitID: 1, Name: hash, AnimeID: 7, EpKey: ep,
			FileIndex: index, FilePath: hash + ".mkv", TotalSize: 100,
		}); err != nil {
			t.Fatal(err)
		}
	}

	record("old", 0, "1")
	if _, err := s.KeepDownload(ctx, "old", true); err != nil {
		t.Fatal(err)
	}
	record("new", 0, "1")

	kept := map[string]bool{}
	entries, _ := s.CacheEntries(ctx)
	for _, e := range entries {
		kept[e.InfoHash] = e.Kept
	}
	if !kept["new"] {
		t.Errorf("the replacement lost the keep: %+v", entries)
	}
	if gone, _ := s.Superseded(ctx, 7, "1"); len(gone) != 1 || gone[0] != "old" {
		t.Errorf("superseded = %v, want [old]", gone)
	}

	// A batch still holding another episode is not superseded by losing one.
	record("batch", 0, "2")
	record("batch", 1, "3")
	record("solo", 0, "2")
	if gone, _ := s.Superseded(ctx, 7, "2"); len(gone) != 0 {
		t.Errorf("a batch with episode 3 still selected was reported: %v", gone)
	}
}

// Seen means finished in the player or covered by the list's progress.
func TestWatchedEpisodesCombinePlayerAndList(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if err := s.EnsureAnime(ctx, 7); err != nil {
		t.Fatal(err)
	}

	// Reported the way a player does, since played time is capped per report.
	var done bool
	for i := 1; i <= 40 && !done; i++ {
		var err error
		done, err = s.SavePlayback(ctx, PlaybackState{
			AnimeID: 7, EpKey: "5", Position: float64(i) * 35, Duration: 1400, Played: 35,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if !done {
		t.Fatal("playing to the end did not count as watched")
	}
	if _, err := s.MarkWatched(ctx, 7, 2); err != nil {
		t.Fatal(err)
	}

	got, err := s.WatchedEpisodes(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{1, 2, 5} {
		if !got[n] {
			t.Errorf("episode %d should count as seen: %v", n, got)
		}
	}
	for _, n := range []int{3, 4, 6} {
		if got[n] {
			t.Errorf("episode %d should not count as seen: %v", n, got)
		}
	}
}
