package store

import (
	"context"
	"testing"
)

func seedShow(t *testing.T, s *Store, id, episodes int) {
	t.Helper()
	_, err := s.w.Exec(
		`INSERT INTO anime (id, title_romaji, episode_count, synced_at) VALUES (?, 'Show', ?, 0)
		 ON CONFLICT(id) DO UPDATE SET episode_count = excluded.episode_count`, id, episodes)
	if err != nil {
		t.Fatal(err)
	}
}

func entryOf(t *testing.T, s *Store, animeID int) DirtyEntry {
	t.Helper()
	got, err := s.ListEntry(context.Background(), animeID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestMarkWatchedCreatesEntry(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedShow(t, s, 1, 12)

	changed, err := s.MarkWatched(ctx, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("first episode should create an entry")
	}

	got := entryOf(t, s, 1)
	if got.Progress != 1 || got.Status != "CURRENT" {
		t.Fatalf("entry = %+v", got)
	}
	if got.StartedAt == "" {
		t.Error("started date not set on the first episode")
	}

	dirty, _ := s.DirtyEntries(ctx, 0)
	if len(dirty) != 1 {
		t.Fatalf("%d dirty entries, want 1", len(dirty))
	}
}

// Rewatching an early episode of a finished series must not reset the count.
func TestMarkWatchedOnlyMovesForward(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedShow(t, s, 1, 12)

	s.MarkWatched(ctx, 1, 8)
	if got := entryOf(t, s, 1).Progress; got != 8 {
		t.Fatalf("progress = %d", got)
	}

	changed, err := s.MarkWatched(ctx, 1, 3)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("rewatching an earlier episode should not count as progress")
	}
	if got := entryOf(t, s, 1).Progress; got != 8 {
		t.Fatalf("progress went backwards to %d", got)
	}
}

// AniList applies no such rule server-side, so the client has to complete the
// entry itself.
func TestMarkWatchedCompletesOnFinalEpisode(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedShow(t, s, 1, 12)

	s.MarkWatched(ctx, 1, 11)
	if got := entryOf(t, s, 1).Status; got != "CURRENT" {
		t.Fatalf("status = %q before the last episode", got)
	}

	s.MarkWatched(ctx, 1, 12)
	got := entryOf(t, s, 1)
	if got.Status != "COMPLETED" {
		t.Fatalf("status = %q on the final episode", got.Status)
	}
	if got.CompletedAt == "" {
		t.Error("completion date not recorded")
	}
}

// A currently-airing show has no episode count, and guessing one would
// complete the entry after the first episode.
func TestMarkWatchedDoesNotCompleteWithUnknownLength(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedShow(t, s, 1, 0)

	s.MarkWatched(ctx, 1, 5)
	if got := entryOf(t, s, 1).Status; got != "CURRENT" {
		t.Fatalf("status = %q; an unknown length must not complete the entry", got)
	}
}

// Writing back the server's own timestamp is what stops the next pull from
// seeing our write as a remote change.
func TestClearDirtyRecordsRemoteState(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedShow(t, s, 1, 12)

	s.MarkWatched(ctx, 1, 3)
	if err := s.ClearDirty(ctx, 1, 40942016, 1786000000); err != nil {
		t.Fatal(err)
	}

	if dirty, _ := s.DirtyEntries(ctx, 0); len(dirty) != 0 {
		t.Fatalf("%d entries still dirty", len(dirty))
	}

	var remoteID, updatedAt int
	s.r.QueryRowContext(ctx,
		`SELECT id, remote_updated_at FROM list_entry WHERE anime_id = 1`).Scan(&remoteID, &updatedAt)
	if remoteID != 40942016 {
		t.Errorf("remote id = %d", remoteID)
	}
	if updatedAt != 1786000000 {
		t.Errorf("remote timestamp = %d", updatedAt)
	}
}

func TestMarkWatchedIgnoresInvalidInput(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	for _, tc := range [][2]int{{0, 1}, {1, 0}, {-1, -1}} {
		if changed, err := s.MarkWatched(ctx, tc[0], tc[1]); changed || err != nil {
			t.Errorf("MarkWatched(%d, %d) = %v, %v", tc[0], tc[1], changed, err)
		}
	}
}

// Completing a show already marked completed must not flip it back.
func TestMarkWatchedKeepsCompletedStatus(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedShow(t, s, 1, 12)

	s.MarkWatched(ctx, 1, 12)
	s.MarkWatched(ctx, 1, 12)

	if got := entryOf(t, s, 1).Status; got != "COMPLETED" {
		t.Fatalf("status = %q", got)
	}
}
