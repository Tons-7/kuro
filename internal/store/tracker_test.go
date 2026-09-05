package store

import (
	"context"
	"testing"
)

// seedTracked writes the minimum needed for a list entry to exist and be
// resolvable to a MyAnimeList id.
func seedTracked(t *testing.T, s *Store, animeID, malID, episodes int) {
	t.Helper()
	ctx := context.Background()

	var mal any
	if malID > 0 {
		mal = malID
	}
	_, err := s.w.ExecContext(ctx,
		`INSERT INTO anime (id, mal_id, title_romaji, episode_count, synced_at)
		 VALUES (?, ?, ?, ?, 0)
		 ON CONFLICT(id) DO UPDATE SET mal_id = excluded.mal_id`,
		animeID, mal, "Show", episodes)
	if err != nil {
		t.Fatal(err)
	}
}

func watch(t *testing.T, s *Store, animeID, episode int) {
	t.Helper()
	if _, err := s.MarkWatched(context.Background(), animeID, episode); err != nil {
		t.Fatal(err)
	}
}

func TestPendingMALPushesFindsUnsentProgress(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	seedTracked(t, s, 154587, 52991, 28)
	watch(t, s, 154587, 10)

	pending, err := s.PendingMALPushes(ctx, "mal", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("got %d pending, want 1", len(pending))
	}

	got := pending[0]
	if got.AnimeID != 154587 || got.RemoteID != 52991 {
		t.Errorf("entry = %+v, want the MAL id resolved", got)
	}
	if got.Progress != 10 || got.Status != "CURRENT" {
		t.Errorf("progress/status = %d/%s", got.Progress, got.Status)
	}
}

// Anime that MAL has no id for cannot be updated there, so they must not sit
// in the queue failing forever.
func TestPendingMALPushesSkipsUnmappedAnime(t *testing.T) {
	s := newTestStore(t)

	seedTracked(t, s, 999001, 0, 12)
	watch(t, s, 999001, 3)

	pending, err := s.PendingMALPushes(context.Background(), "mal", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("got %d pending for an anime with no MAL id", len(pending))
	}
}

func TestMarkPushedClearsAndLocalChangeReopens(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	seedTracked(t, s, 154587, 52991, 28)
	watch(t, s, 154587, 10)

	if err := s.MarkPushed(ctx, "mal", 154587, 10, "CURRENT", 0); err != nil {
		t.Fatal(err)
	}
	pending, _ := s.PendingMALPushes(ctx, "mal", 50)
	if len(pending) != 0 {
		t.Fatalf("still %d pending after pushing", len(pending))
	}

	// Watching one more episode has to make it pending again.
	watch(t, s, 154587, 11)
	pending, _ = s.PendingMALPushes(ctx, "mal", 50)
	if len(pending) != 1 {
		t.Fatalf("got %d pending after new progress, want 1", len(pending))
	}
	if pending[0].Progress != 11 {
		t.Errorf("progress = %d", pending[0].Progress)
	}
}

// A status change with no progress change still has to reach the tracker.
func TestStatusChangeAloneIsPending(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	seedTracked(t, s, 154587, 52991, 28)
	watch(t, s, 154587, 10)
	s.MarkPushed(ctx, "mal", 154587, 10, "CURRENT", 0)

	if _, err := s.w.ExecContext(ctx,
		`UPDATE list_entry SET status = 'DROPPED' WHERE anime_id = ?`, 154587); err != nil {
		t.Fatal(err)
	}

	pending, _ := s.PendingMALPushes(ctx, "mal", 50)
	if len(pending) != 1 {
		t.Fatalf("got %d pending, want the status change", len(pending))
	}
	if pending[0].Status != "DROPPED" {
		t.Errorf("status = %s", pending[0].Status)
	}
}

func TestPendingMALPushTargetsOneAnime(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	seedTracked(t, s, 1, 101, 12)
	seedTracked(t, s, 2, 102, 12)
	watch(t, s, 1, 5)
	watch(t, s, 2, 7)

	got, err := s.PendingMALPush(ctx, "mal", 2)
	if err != nil {
		t.Fatal(err)
	}
	if got.AnimeID != 2 || got.RemoteID != 102 || got.Progress != 7 {
		t.Fatalf("entry = %+v", got)
	}

	s.MarkPushed(ctx, "mal", 2, 7, "CURRENT", 0)
	got, err = s.PendingMALPush(ctx, "mal", 2)
	if err != nil {
		t.Fatal(err)
	}
	if got.AnimeID != 0 {
		t.Errorf("entry = %+v, want empty once pushed", got)
	}
}

// Per-tracker state must be independent, or connecting MAL would look like
// AniList had already been told.
func TestTrackersDoNotShareState(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	seedTracked(t, s, 154587, 52991, 28)
	watch(t, s, 154587, 10)
	s.MarkPushed(ctx, "anilist", 154587, 10, "CURRENT", 0)

	pending, _ := s.PendingMALPushes(ctx, "mal", 50)
	if len(pending) != 1 {
		t.Fatalf("got %d pending for mal after an anilist push", len(pending))
	}
}

// Connecting an account must not queue the entire list for re-upload.
func TestSeedPushedSuppressesRedundantPushes(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	seedTracked(t, s, 1, 101, 12)
	seedTracked(t, s, 2, 102, 24)
	watch(t, s, 1, 5)
	watch(t, s, 2, 7)

	// MAL already agrees about anime 1 but is behind on anime 2.
	n, err := s.SeedPushed(ctx, "mal", []TrackerEntry{
		{AnimeID: 1, Progress: 5, Status: "CURRENT"},
		{AnimeID: 2, Progress: 3, Status: "CURRENT"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("seeded %d rows, want 2", n)
	}

	pending, _ := s.PendingMALPushes(ctx, "mal", 50)
	if len(pending) != 1 {
		t.Fatalf("got %d pending, want only the one MAL is behind on", len(pending))
	}
	if pending[0].AnimeID != 2 {
		t.Errorf("pending anime = %d, want 2", pending[0].AnimeID)
	}
}

func TestSeedPushedIgnoresUnresolvedEntries(t *testing.T) {
	s := newTestStore(t)

	n, err := s.SeedPushed(context.Background(), "mal", []TrackerEntry{{AnimeID: 0, Progress: 5}})
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("seeded %d rows for an unresolved entry", n)
	}
}

func TestAniListIDsForMALResolvesBothTables(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	seedTracked(t, s, 154587, 52991, 28)
	if _, err := s.SaveCorpus(ctx, []CorpusEntry{
		{AniListID: 16498, MalID: 16498, Kind: "TV", Episodes: 25},
	}); err != nil {
		t.Fatal(err)
	}

	got, err := s.AniListIDsForMAL(ctx, []int{52991, 16498, 999999})
	if err != nil {
		t.Fatal(err)
	}
	if got[52991] != 154587 {
		t.Errorf("mal 52991 -> %d, want 154587 from the anime table", got[52991])
	}
	if got[16498] != 16498 {
		t.Errorf("mal 16498 -> %d, want 16498 from the corpus", got[16498])
	}
	if _, ok := got[999999]; ok {
		t.Error("an unknown MAL id was resolved")
	}
}

func TestAniListIDsForMALHandlesEmptyInput(t *testing.T) {
	s := newTestStore(t)
	got, err := s.AniListIDsForMAL(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d mappings", len(got))
	}
}
