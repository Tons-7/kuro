package store

import (
	"context"
	"testing"

	"kuro/internal/metadata"
)

func playbackWatched(t *testing.T, s *Store, animeID int, key string) (watched bool, position float64) {
	t.Helper()
	var w int
	err := s.r.QueryRow(`SELECT watched, position_s FROM playback WHERE anime_id = ? AND ep_key = ?`,
		animeID, key).Scan(&w, &position)
	if err != nil {
		t.Fatal(err)
	}
	return w == 1, position
}

func TestUnwatchRewindsProgressAndClearsLaterTicks(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	ep := 12
	if _, err := s.ImportList(ctx, []Anime{{ID: 1, Romaji: "Show", Episodes: &ep}}, nil, ImportMerge); err != nil {
		t.Fatal(err)
	}
	for n := 1; n <= 5; n++ {
		// One report may claim at most two minutes played; the flag needs half
		// the episode.
		for i := 0; i < 8; i++ {
			if _, err := s.SavePlayback(ctx, PlaybackState{AnimeID: 1, EpKey: epKey(n), Position: 1400, Duration: 1440, Played: 120}); err != nil {
				t.Fatal(err)
			}
		}
		if watched, _ := playbackWatched(t, s, 1, epKey(n)); !watched {
			t.Fatalf("precondition: episode %d not watched", n)
		}
		s.MarkWatched(ctx, 1, n)
	}

	if err := s.Unwatch(ctx, 1, 3); err != nil {
		t.Fatal(err)
	}

	e := entryOf(t, s, 1)
	if e.Progress != 2 || e.Status != "CURRENT" {
		t.Fatalf("entry = %+v, want progress 2", e)
	}
	for n, want := range map[int]bool{1: true, 2: true, 3: false, 4: false, 5: false} {
		if got, _ := playbackWatched(t, s, 1, epKey(n)); got != want {
			t.Errorf("episode %d watched = %v, want %v", n, got, want)
		}
	}
	if _, pos := playbackWatched(t, s, 1, "3"); pos != 0 {
		t.Errorf("episode 3 kept a resume point of %v", pos)
	}
	if dirty, _ := s.DirtyEntries(ctx, 0); len(dirty) != 1 {
		t.Fatalf("rewind must be pushed; dirty = %v", dirty)
	}
}

func TestUnwatchReopensACompletedShow(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	ep := 3
	s.ImportList(ctx, []Anime{{ID: 1, Romaji: "Show", Episodes: &ep}}, nil, ImportMerge)
	for n := 1; n <= 3; n++ {
		s.MarkWatched(ctx, 1, n)
	}
	if e := entryOf(t, s, 1); e.Status != "COMPLETED" {
		t.Fatalf("precondition: status = %q", e.Status)
	}

	if err := s.Unwatch(ctx, 1, 3); err != nil {
		t.Fatal(err)
	}
	e := entryOf(t, s, 1)
	if e.Status != "CURRENT" || e.Progress != 2 || e.CompletedAt != "" {
		t.Fatalf("entry = %+v", e)
	}
}

func TestUnwatchPastProgressChangesNothing(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	s.ImportList(ctx, []Anime{{ID: 1, Romaji: "Show"}}, nil, ImportMerge)
	s.MarkWatched(ctx, 1, 2)
	s.ClearDirty(ctx, 1, 0, 0)

	if err := s.Unwatch(ctx, 1, 7); err != nil {
		t.Fatal(err)
	}
	if e := entryOf(t, s, 1); e.Progress != 2 {
		t.Fatalf("progress = %d, want 2", e.Progress)
	}
	if dirty, _ := s.DirtyEntries(ctx, 0); len(dirty) != 0 {
		t.Fatal("nothing changed, nothing should be pushed")
	}
}

func TestSetScoreCreatesAPlannedEntryAndPushes(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	s.ImportList(ctx, []Anime{{ID: 1, Romaji: "Show"}}, nil, ImportMerge)

	if err := s.SetScore(ctx, 1, 85); err != nil {
		t.Fatal(err)
	}
	e := entryOf(t, s, 1)
	if e.Score != 85 || e.Status != "PLANNING" {
		t.Fatalf("entry = %+v", e)
	}
	dirty, _ := s.DirtyEntries(ctx, 0)
	if len(dirty) != 1 || dirty[0].Score != 85 {
		t.Fatalf("dirty = %+v", dirty)
	}

	// An existing entry keeps its status.
	s.SetListStatus(ctx, 1, "CURRENT", -1)
	if err := s.SetScore(ctx, 1, 0); err != nil {
		t.Fatal(err)
	}
	if e := entryOf(t, s, 1); e.Score != 0 || e.Status != "CURRENT" {
		t.Fatalf("entry = %+v", e)
	}
	if err := s.SetScore(ctx, 1, 101); err != nil {
		t.Fatal(err)
	}
	if e := entryOf(t, s, 1); e.Score != 0 {
		t.Fatal("out-of-range score was stored")
	}
}

func TestLocalEntryIDsNeverCollideWithRealOnes(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	s.ImportList(ctx, []Anime{{ID: 1, Romaji: "A"}, {ID: 2, Romaji: "B"}, {ID: 3, Romaji: "C"}},
		[]Entry{{ID: 500, AnimeID: 1, Progress: 1}}, ImportMerge)

	// max(id)-1 would have handed anime 2 the id 499, a real AniList id.
	s.MarkWatched(ctx, 2, 1)
	s.SetScore(ctx, 3, 50)

	for _, id := range []int{2, 3} {
		if e := entryOf(t, s, id); e.ID >= 0 {
			t.Fatalf("anime %d got local id %d, want negative", id, e.ID)
		}
	}
	if entryOf(t, s, 2).ID == entryOf(t, s, 3).ID {
		t.Fatal("two local rows share an id")
	}
}

func TestDirtyEntriesSkipMALOnlyAnime(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	s.MarkWatched(ctx, 1, 3)
	// A row with a negative anime id, however it got there, is not AniList's.
	if _, err := s.w.ExecContext(ctx, `INSERT INTO anime (id, title_romaji, synced_at) VALUES (-52991, 'MAL only', 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.w.ExecContext(ctx, `INSERT INTO list_entry (id, anime_id, progress, dirty) VALUES (-9, -52991, 3, 1)`); err != nil {
		t.Fatal(err)
	}

	dirty, err := s.DirtyEntries(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirty) != 1 || dirty[0].AnimeID != 1 {
		t.Fatalf("dirty = %+v, want only the AniList anime", dirty)
	}
}

func TestApplyRemoteCreatesAndUpdatesButNeverOverwritesLocalEdits(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	s.ImportList(ctx, []Anime{{ID: 1, Romaji: "Show"}}, nil, ImportMerge)

	changed, err := s.ApplyRemote(ctx, RemoteEntry{AnimeID: 1, Status: "CURRENT", Progress: 4, Score: 70, Repeat: 1})
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	e := entryOf(t, s, 1)
	if e.Progress != 4 || e.Score != 70 || e.Repeat != 1 || e.Status != "CURRENT" {
		t.Fatalf("entry = %+v", e)
	}
	if dirty, _ := s.DirtyEntries(ctx, 0); len(dirty) != 1 {
		t.Fatal("a remote change must be flagged for the other tracker")
	}

	// Unpushed: the remote must not roll it back.
	changed, err = s.ApplyRemote(ctx, RemoteEntry{AnimeID: 1, Status: "CURRENT", Progress: 2})
	if err != nil || changed {
		t.Fatalf("changed=%v err=%v; a dirty row was overwritten", changed, err)
	}
	if e := entryOf(t, s, 1); e.Progress != 4 {
		t.Fatalf("progress = %d after refused apply", e.Progress)
	}

	s.ClearDirty(ctx, 1, 9, 1)
	changed, err = s.ApplyRemote(ctx, RemoteEntry{AnimeID: 1, Status: "COMPLETED", Progress: 12, Score: 70, Repeat: 0})
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	e = entryOf(t, s, 1)
	if e.Status != "COMPLETED" || e.Progress != 12 || e.CompletedAt == "" || e.Repeat != 1 {
		t.Fatalf("entry = %+v; repeat must not go backwards", e)
	}

	// Unknown vocabulary keeps the status rather than storing garbage.
	s.ClearDirty(ctx, 1, 9, 2)
	if _, err := s.ApplyRemote(ctx, RemoteEntry{AnimeID: 1, Status: "weird", Progress: 12, Score: 70}); err != nil {
		t.Fatal(err)
	}
	if e := entryOf(t, s, 1); e.Status != "COMPLETED" {
		t.Fatalf("status = %q", e.Status)
	}
}

func TestBookmarkRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	b, err := s.Bookmark(ctx, 1)
	if err != nil || b.Favourite || b.Note != "" {
		t.Fatalf("absent bookmark = %+v, %v", b, err)
	}
	if err := s.SetBookmark(ctx, 1, Bookmark{Favourite: true, Note: "rewatch in winter"}); err != nil {
		t.Fatal(err)
	}
	b, _ = s.Bookmark(ctx, 1)
	if !b.Favourite || b.Note != "rewatch in winter" {
		t.Fatalf("bookmark = %+v", b)
	}
	page, err := s.Bookmarks(ctx, Paging{Page: 1, PerPage: 10})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != 1 {
		t.Fatalf("favourites = %+v, %v", page, err)
	}
	s.SetBookmark(ctx, 1, Bookmark{Favourite: false, Note: "rewatch in winter"})
	if page, _ := s.Bookmarks(ctx, Paging{Page: 1, PerPage: 10}); len(page.Items) != 0 {
		t.Fatal("unfavourited show still listed")
	}
}

func epKey(n int) string {
	return string(rune('0' + n))
}

// A tick set by hand raises the list's progress, not the player's flag, so the
// episode list has to read both — or marking by hand never shows.
func TestEpisodeListTicksFollowProgressAndUnwatch(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	ep := 4
	s.ImportList(ctx, []Anime{{ID: 1, Romaji: "Show", Episodes: &ep}}, nil, ImportMerge)
	s.SaveEpisodes(ctx, 1, []metadata.Episode{{Number: 1}, {Number: 2}, {Number: 3}, {Number: 4}})

	s.MarkWatched(ctx, 1, 3)
	rows, err := s.Episodes(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.Watched != (r.Number <= 3) {
			t.Errorf("episode %d watched = %v after progress 3", r.Number, r.Watched)
		}
	}

	s.Unwatch(ctx, 1, 2)
	rows, _ = s.Episodes(ctx, 1)
	for _, r := range rows {
		if r.Watched != (r.Number <= 1) {
			t.Errorf("episode %d watched = %v after unwatching 2", r.Number, r.Watched)
		}
	}

	// The same rule for a show with no episode data, only a count.
	s.ImportList(ctx, []Anime{{ID: 2, Romaji: "Bare", Episodes: &ep}}, nil, ImportMerge)
	s.MarkWatched(ctx, 2, 2)
	planned, _ := s.Episodes(ctx, 2)
	if len(planned) != 4 || !planned[1].Watched || planned[2].Watched {
		t.Fatalf("planned rows = %+v", planned)
	}
}
