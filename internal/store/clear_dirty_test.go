package store

import (
	"context"
	"testing"
)

// A push reads the entry, sends it, then clears dirty. An edit that landed
// meanwhile must stay dirty, or it is never sent and the next pull undoes it.
func TestClearDirtyKeepsAnEditMadeDuringThePush(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedCatalogue(t, s, 1, "FINISHED", 12)
	if _, err := s.MarkWatched(ctx, 1, 5); err != nil {
		t.Fatal(err)
	}

	snapshot, err := s.ListEntry(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	// Watched on while the push was in flight.
	if _, err := s.w.ExecContext(ctx,
		`UPDATE list_entry SET progress = 6, local_updated_at = ?, dirty = 1 WHERE anime_id = 1`,
		snapshot.Changed+5); err != nil {
		t.Fatal(err)
	}

	if err := s.ClearDirtyAt(ctx, 1, 10, 100, snapshot.Changed); err != nil {
		t.Fatal(err)
	}
	after, _ := s.ListEntry(ctx, 1)
	if !after.Dirty {
		t.Fatal("the later edit was marked clean and would never be pushed")
	}

	if err := s.ClearDirtyAt(ctx, 1, 10, 100, after.Changed); err != nil {
		t.Fatal(err)
	}
	if done, _ := s.ListEntry(ctx, 1); done.Dirty {
		t.Fatal("a push of the latest state should clear it")
	}
}

// A list fetched before our last push must not undo it: the push stored the
// server's updatedAt, and an older remote row is skipped.
func TestMergeSkipsARemoteRowOlderThanOurPush(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedCatalogue(t, s, 1, "RELEASING", 12)
	status := "CURRENT"

	if _, err := s.ImportList(ctx, nil, []Entry{{ID: 50, AnimeID: 1, Status: &status, Progress: 6, UpdatedAt: 1000}}, ImportMerge); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkWatched(ctx, 1, 7); err != nil {
		t.Fatal(err)
	}
	e, _ := s.ListEntry(ctx, 1)
	if err := s.ClearDirtyAt(ctx, 1, 50, 2000, e.Changed); err != nil {
		t.Fatal(err)
	}

	// The stale fetch: progress 6 as of 1000.
	if _, err := s.ImportList(ctx, nil, []Entry{{ID: 50, AnimeID: 1, Status: &status, Progress: 6, UpdatedAt: 1000}}, ImportMerge); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.ListEntry(ctx, 1); got.Progress != 7 {
		t.Fatalf("progress = %d, want 7 kept over an older list", got.Progress)
	}

	// A real edit on the site since then still lands.
	if _, err := s.ImportList(ctx, nil, []Entry{{ID: 50, AnimeID: 1, Status: &status, Progress: 9, UpdatedAt: 3000}}, ImportMerge); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.ListEntry(ctx, 1); got.Progress != 9 {
		t.Fatalf("progress = %d, want the newer site edit 9", got.Progress)
	}
}

// Unrated on purpose is flagged, so the push sends the 0; a new local row's 0
// is not, so it can't wipe the site's score.
func TestUnratingIsFlaggedUntilPushed(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedCatalogue(t, s, 1, "FINISHED", 12)

	if _, err := s.MarkWatched(ctx, 1, 1); err != nil {
		t.Fatal(err)
	}
	if e, _ := s.ListEntry(ctx, 1); e.ScoreCleared {
		t.Fatal("a row that was never scored claims to be cleared")
	}
	if err := s.SetScore(ctx, 1, 80); err != nil {
		t.Fatal(err)
	}
	if err := s.SetScore(ctx, 1, 0); err != nil {
		t.Fatal(err)
	}
	e, _ := s.ListEntry(ctx, 1)
	if !e.ScoreCleared || e.Score != 0 {
		t.Fatalf("entry = %+v, want an explicit clear", e)
	}
	if err := s.ClearDirtyAt(ctx, 1, 10, 100, e.Changed); err != nil {
		t.Fatal(err)
	}
	if e, _ := s.ListEntry(ctx, 1); e.ScoreCleared {
		t.Fatal("still flagged after the push landed")
	}
}
