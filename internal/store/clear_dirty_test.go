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

// A list fetched before our last push can't undo it: rows older than its stored updatedAt are skipped.
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

// A deliberate unrate is flagged so its 0 is pushed; a new row's 0 isn't, so it can't wipe the site's.
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

// A row the tracker keeps refusing goes to the back of the line, so newer edits still get their turn.
func TestRefusedPushesGoToTheBackOfTheLine(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for id := 1; id <= 3; id++ {
		seedShow(t, s, id, 12)
		if _, err := s.MarkWatched(ctx, id, id); err != nil {
			t.Fatal(err)
		}
	}
	order := func(tracker string) []int {
		var ids []int
		if tracker == "anilist" {
			dirty, _ := s.DirtyEntries(ctx, 0)
			for _, d := range dirty {
				ids = append(ids, d.AnimeID)
			}
			return ids
		}
		pending, _ := s.PendingMALPushes(ctx, tracker, 0)
		for _, p := range pending {
			ids = append(ids, p.AnimeID)
		}
		return ids
	}

	s.PushFailed(ctx, "anilist", 1)
	if got := order("anilist"); len(got) != 3 || got[2] != 1 {
		t.Fatalf("anilist order = %v, want the refused row last", got)
	}
	if err := s.ClearDirty(ctx, 1, 0, 1); err != nil {
		t.Fatal(err)
	}
	var failed int64
	s.r.QueryRow(`SELECT anilist_failed_at FROM list_entry WHERE anime_id = 1`).Scan(&failed)
	if failed != 0 {
		t.Fatal("a successful AniList push must forget the refusal")
	}

	for id := 1; id <= 3; id++ {
		s.w.Exec(`UPDATE anime SET mal_id = ? WHERE id = ?`, 100+id, id)
	}
	s.PushFailed(ctx, "mal", 2)
	if got := order("mal"); got[len(got)-1] != 2 {
		t.Fatalf("mal order = %v, want the refused row last", got)
	}
	s.MarkPushed(ctx, "mal", 2, 2, "CURRENT", 0)
	s.r.QueryRow(`SELECT mal_failed_at FROM list_entry WHERE anime_id = 2`).Scan(&failed)
	if failed != 0 {
		t.Fatal("a successful MAL push must forget the refusal")
	}
}

// An entry with no status and no progress has nothing MAL can hold, so it never takes a slot.
func TestEmptyEntriesAreNotPendingForMAL(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedShow(t, s, 1, 12)
	s.w.Exec(`UPDATE anime SET mal_id = 101 WHERE id = 1`)
	s.w.Exec(`INSERT INTO list_entry (id, anime_id, progress, remote_updated_at, local_updated_at, dirty) VALUES (-1, 1, 0, 0, 1, 1)`)
	if pending, _ := s.PendingMALPushes(ctx, "mal", 0); len(pending) != 0 {
		t.Fatalf("pending = %+v", pending)
	}
}
