package store

import (
	"context"
	"testing"
)

func rewatchEntry(t *testing.T, s *Store, animeID int) (status string, progress, repeat int, dirty int) {
	t.Helper()
	err := s.r.QueryRow(
		`SELECT coalesce(status,''), progress, repeat_count, dirty FROM list_entry WHERE anime_id = ?`,
		animeID).Scan(&status, &progress, &repeat, &dirty)
	if err != nil {
		t.Fatal(err)
	}
	return
}

// Choosing "Rewatching" on a finished show starts over: progress back to zero so
// "continue" offers episode 1, the episode ticks cleared for this pass, the
// first watch's finish date kept. Re-choosing it mid-rewatch changes nothing.
func TestRewatchStartsOver(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedCatalogue(t, s, 7, "FINISHED", 12)

	// Watched through once.
	for n := 1; n <= 12; n++ {
		if _, err := s.MarkWatched(ctx, 7, n); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.w.Exec(`INSERT INTO playback (anime_id, ep_key, position_s, watched, last_played_at)
		VALUES (7, '3', 1400, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if status, progress, _, _ := rewatchEntry(t, s, 7); status != "COMPLETED" || progress != 12 {
		t.Fatalf("first watch: %s %d, want COMPLETED 12", status, progress)
	}
	var completedAt string
	s.r.QueryRow(`SELECT coalesce(completed_at,'') FROM list_entry WHERE anime_id = 7`).Scan(&completedAt)

	if err := s.SetListStatus(ctx, 7, StatusRepeating, -1); err != nil {
		t.Fatal(err)
	}
	status, progress, repeat, dirty := rewatchEntry(t, s, 7)
	if status != StatusRepeating || progress != 0 || repeat != 0 || dirty != 1 {
		t.Errorf("after starting a rewatch: %s progress=%d repeat=%d dirty=%d, want REPEATING 0 0 dirty", status, progress, repeat, dirty)
	}
	var kept string
	s.r.QueryRow(`SELECT coalesce(completed_at,'') FROM list_entry WHERE anime_id = 7`).Scan(&kept)
	if kept != completedAt || kept == "" {
		t.Errorf("the first finish date was lost: %q -> %q", completedAt, kept)
	}
	var watched int
	var position float64
	s.r.QueryRow(`SELECT coalesce(sum(watched),0), coalesce(max(position_s),0) FROM playback WHERE anime_id = 7`).
		Scan(&watched, &position)
	if watched != 0 || position != 0 {
		t.Errorf("the first watch's ticks/positions survived: watched=%d position=%v", watched, position)
	}
	// Nothing part way, so "continue" is episode 1, not a leftover position.
	if _, ok, _ := s.LastInProgress(ctx, 7); ok {
		t.Error("a rewatch must not resume a first-watch position")
	}

	// Part way through, choosing it again must not wipe the new progress.
	if _, err := s.MarkWatched(ctx, 7, 4); err != nil {
		t.Fatal(err)
	}
	if err := s.SetListStatus(ctx, 7, StatusRepeating, -1); err != nil {
		t.Fatal(err)
	}
	if status, progress, _, _ := rewatchEntry(t, s, 7); status != StatusRepeating || progress != 4 {
		t.Errorf("re-choosing rewatching reset it: %s %d", status, progress)
	}
}

// Finishing a rewatch completes the entry again and counts the rewatch, which
// is what both trackers show as "rewatched N times".
func TestFinishingARewatchCountsIt(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedCatalogue(t, s, 7, "FINISHED", 3)

	for n := 1; n <= 3; n++ {
		if _, err := s.MarkWatched(ctx, 7, n); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, repeat, _ := rewatchEntry(t, s, 7); repeat != 0 {
		t.Fatalf("a first watch counted as a rewatch: %d", repeat)
	}

	for round := 1; round <= 2; round++ {
		if err := s.SetListStatus(ctx, 7, StatusRepeating, -1); err != nil {
			t.Fatal(err)
		}
		for n := 1; n <= 3; n++ {
			if _, err := s.MarkWatched(ctx, 7, n); err != nil {
				t.Fatal(err)
			}
		}
		status, progress, repeat, _ := rewatchEntry(t, s, 7)
		if status != "COMPLETED" || progress != 3 || repeat != round {
			t.Errorf("after rewatch %d: %s %d repeat=%d", round, status, progress, repeat)
		}
	}

	// The count travels to both trackers.
	entry, err := s.ListEntry(ctx, 7)
	if err != nil || entry.Repeat != 2 {
		t.Errorf("ListEntry repeat = %d (%v), want 2", entry.Repeat, err)
	}
	dirty, err := s.DirtyEntries(ctx, 0)
	if err != nil || len(dirty) != 1 || dirty[0].Repeat != 2 {
		t.Errorf("DirtyEntries = %+v (%v), want one with repeat 2", dirty, err)
	}
	if _, err := s.w.Exec(`UPDATE anime SET mal_id = 99 WHERE id = 7`); err != nil {
		t.Fatal(err)
	}
	pending, err := s.PendingMALPush(ctx, "mal", 7)
	if err != nil || pending.Repeat != 2 {
		t.Errorf("PendingMALPush repeat = %d (%v), want 2", pending.Repeat, err)
	}
}

// A count imported from the tracker is kept and reported.
func TestImportKeepsTheRewatchCount(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	anime := []Anime{{ID: 7, Romaji: "Show", Synonyms: "[]", Genres: "[]"}}
	rows := []Entry{{ID: 500, AnimeID: 7, Status: current(), Progress: 12, Repeat: 3}}
	if _, err := s.ImportList(ctx, anime, rows, ImportReplace); err != nil {
		t.Fatal(err)
	}
	if entry, err := s.ListEntry(ctx, 7); err != nil || entry.Repeat != 3 {
		t.Errorf("repeat = %d (%v), want 3", entry.Repeat, err)
	}
}
