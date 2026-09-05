package store

import (
	"context"
	"testing"
)

func play(t *testing.T, s *Store, animeID int, ep string, pos, dur float64) {
	t.Helper()
	seedAnime(t, s, animeID)
	playThrough(t, s, animeID, ep, 0, pos, dur)
}

// playThrough reports the way a player does — every few seconds, each report
// carrying what played since the last — from one position to another.
func playThrough(t *testing.T, s *Store, animeID int, ep string, from, to, dur float64) bool {
	t.Helper()
	const step = 10.0
	var watched bool
	for at := from; ; at += step {
		report := min(at, to)
		played := min(step, report-(at-step))
		if at == from {
			played = 0
		}
		w, err := s.SavePlayback(context.Background(), PlaybackState{
			AnimeID: animeID, EpKey: ep, Position: report, Duration: dur, Played: played,
		})
		if err != nil {
			t.Fatal(err)
		}
		watched = watched || w
		if report >= to {
			return watched
		}
	}
}

func TestDismissHidesFromContinueWatching(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	all := Paging{Page: 1, PerPage: 50}

	play(t, s, 1, "3", 300, 1400)

	page, err := s.ContinueWatching(ctx, all)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("got %d items before dismissing, want 1", len(page.Items))
	}

	if err := s.DismissResume(ctx, 1, "3"); err != nil {
		t.Fatal(err)
	}

	page, err = s.ContinueWatching(ctx, all)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 0 {
		t.Fatalf("still listed after dismissing: %+v", page.Items)
	}
	if page.Total != 0 {
		t.Errorf("total = %d, want 0", page.Total)
	}
}

func TestDismissDoesNotMarkTheEpisodeWatched(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	play(t, s, 1, "3", 300, 1400)
	if err := s.DismissResume(ctx, 1, "3"); err != nil {
		t.Fatal(err)
	}

	var watched int
	err := s.r.QueryRowContext(ctx,
		`SELECT watched FROM playback WHERE anime_id = 1 AND ep_key = '3'`).Scan(&watched)
	if err != nil {
		t.Fatal(err)
	}
	if watched != 0 {
		t.Fatal("dismissing put a watched tick against an episode that was not finished")
	}
}

func TestHistoryListsEpisodesNewestFirst(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	play(t, s, 1, "1", 1400, 1400)
	play(t, s, 1, "2", 120, 1400)

	page, err := s.History(ctx, Paging{Page: 1, PerPage: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("got %d entries, want both plays", len(page.Items))
	}
	if page.Items[0].Episode != 2 {
		t.Errorf("first entry is episode %d, want the most recent", page.Items[0].Episode)
	}
	if !page.Items[1].Watched {
		t.Error("a fully played episode is not marked watched")
	}
	if got := page.Items[1].Percent; got < 99 {
		t.Errorf("percent = %.0f, want ~100", got)
	}
}

func TestForgetHistoryScopes(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	all := Paging{Page: 1, PerPage: 50}

	play(t, s, 1, "1", 100, 1400)
	play(t, s, 1, "2", 100, 1400)
	play(t, s, 2, "1", 100, 1400)

	if n, err := s.ForgetHistory(ctx, 1, "1"); err != nil || n != 1 {
		t.Fatalf("forget one = %d, %v", n, err)
	}
	page, _ := s.History(ctx, all)
	if len(page.Items) != 2 {
		t.Fatalf("got %d after forgetting one episode", len(page.Items))
	}

	if n, err := s.ForgetHistory(ctx, 1, ""); err != nil || n != 1 {
		t.Fatalf("forget show = %d, %v", n, err)
	}
	page, _ = s.History(ctx, all)
	if len(page.Items) != 1 || page.Items[0].AnimeID != 2 {
		t.Fatalf("remaining = %+v, want only the other show", page.Items)
	}

	if _, err := s.ForgetHistory(ctx, 0, ""); err != nil {
		t.Fatal(err)
	}
	if page, _ = s.History(ctx, all); len(page.Items) != 0 {
		t.Fatalf("%d entries left after clearing everything", len(page.Items))
	}
}
