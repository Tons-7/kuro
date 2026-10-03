package store

import (
	"context"
	"testing"
)

func addTorrent(t *testing.T, s *Store, hash string, engineID int) {
	t.Helper()
	seedAnime(t, s, 1)

	err := s.RecordTorrent(context.Background(), TorrentRecord{
		InfoHash: hash, EngineID: engineID, Name: "release " + hash,
		TotalSize: 1 << 30, AnimeID: 1, EpKey: "e1", FileIndex: 0, FilePath: "a.mkv",
	})
	if err != nil {
		t.Fatal(err)
	}
}

func engineID(t *testing.T, s *Store, hash string) (int, bool) {
	t.Helper()
	var id *int
	err := s.r.QueryRow(`SELECT engine_id FROM torrent WHERE info_hash = ?`, hash).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	if id == nil {
		return 0, false
	}
	return *id, true
}

// The engine hands out ids per session and reuses them. Trusting an id recorded
// before a restart is how a cache sweep deletes the wrong anime.
func TestReconcileRepointsMovedIDs(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	addTorrent(t, s, "aaaa", 1)
	addTorrent(t, s, "bbbb", 2)

	// After a restart the engine has both, with the numbers swapped.
	matched, orphaned, err := s.ReconcileTorrents(ctx, map[string]int{"aaaa": 2, "bbbb": 1})
	if err != nil {
		t.Fatal(err)
	}
	if matched != 2 || orphaned != 0 {
		t.Fatalf("matched=%d orphaned=%d", matched, orphaned)
	}

	if id, _ := engineID(t, s, "aaaa"); id != 2 {
		t.Errorf("aaaa kept id %d, want 2", id)
	}
	if id, _ := engineID(t, s, "bbbb"); id != 1 {
		t.Errorf("bbbb kept id %d, want 1", id)
	}
}

// A torrent the engine has forgotten must lose its id, so nothing can act on
// a number that now means something else.
func TestReconcileClearsUnknownTorrents(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	addTorrent(t, s, "aaaa", 1)
	addTorrent(t, s, "bbbb", 2)

	matched, orphaned, err := s.ReconcileTorrents(ctx, map[string]int{"aaaa": 7})
	if err != nil {
		t.Fatal(err)
	}
	if matched != 1 || orphaned != 1 {
		t.Fatalf("matched=%d orphaned=%d", matched, orphaned)
	}

	if id, ok := engineID(t, s, "bbbb"); ok {
		t.Errorf("forgotten torrent still carries id %d", id)
	}

	var state string
	if err := s.r.QueryRow(`SELECT state FROM torrent WHERE info_hash = 'bbbb'`).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "gone" {
		t.Errorf("state = %q, want gone", state)
	}
}

// A hash-case difference must not read as "this torrent disappeared".
func TestReconcileIsCaseInsensitive(t *testing.T) {
	s := newTestStore(t)

	addTorrent(t, s, "abcdef", 1)
	matched, orphaned, err := s.ReconcileTorrents(context.Background(), map[string]int{"ABCDEF": 5})
	if err != nil {
		t.Fatal(err)
	}
	if matched != 1 || orphaned != 0 {
		t.Fatalf("matched=%d orphaned=%d", matched, orphaned)
	}
	if id, _ := engineID(t, s, "abcdef"); id != 5 {
		t.Errorf("id = %d, want 5", id)
	}
}

// An empty listing at startup is no evidence that anything is gone.
func TestReconcileLeavesEverythingOnAnEmptyListing(t *testing.T) {
	s := newTestStore(t)

	addTorrent(t, s, "aaaa", 1)
	matched, orphaned, err := s.ReconcileTorrents(context.Background(), map[string]int{})
	if err != nil {
		t.Fatal(err)
	}
	if matched != 0 || orphaned != 0 {
		t.Fatalf("matched=%d orphaned=%d", matched, orphaned)
	}
	if id, ok := engineID(t, s, "aaaa"); !ok || id != 1 {
		t.Fatalf("the stored id was cleared: %d %v", id, ok)
	}
}

func TestReconcileOnEmptyDatabaseIsHarmless(t *testing.T) {
	s := newTestStore(t)

	matched, orphaned, err := s.ReconcileTorrents(context.Background(), map[string]int{"aaaa": 1})
	if err != nil {
		t.Fatal(err)
	}
	if matched != 0 || orphaned != 0 {
		t.Fatalf("matched=%d orphaned=%d", matched, orphaned)
	}
}
