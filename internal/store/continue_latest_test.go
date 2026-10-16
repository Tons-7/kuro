package store

import (
	"context"
	"testing"
)

// An older episode left part way is not what to continue once later ones were watched to the end: the
// rail follows the show's latest episode, as the series page does, and offers the next one.
func TestContinueWatchingFollowsTheLatestEpisode(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	all := Paging{Page: 1, PerPage: 50}
	seedCatalogue(t, s, 1, "FINISHED", 12)

	if err := s.SetListStatus(ctx, 1, StatusCurrent, -1); err != nil {
		t.Fatal(err)
	}
	playThrough(t, s, 1, "3", 0, 600, 1400)
	if _, err := s.w.ExecContext(ctx, `UPDATE playback SET last_played_at = last_played_at - 3600`); err != nil {
		t.Fatal(err)
	}
	for _, ep := range []string{"4", "5"} {
		playThrough(t, s, 1, ep, 0, 1400, 1400)
	}
	if _, err := s.MarkWatched(ctx, 1, 5); err != nil {
		t.Fatal(err)
	}

	page, err := s.ContinueWatching(ctx, all)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("items = %d, want the one show", len(page.Items))
	}
	if r := page.Items[0].Resume; r != nil && r.Position > 0 {
		t.Errorf("resume = %+v; episode 3 was abandoned, not what comes next", r)
	}
	if got := page.Items[0].Progress; got != 5 {
		t.Errorf("progress = %d, want 5 so the card offers episode 6", got)
	}
	if _, ok, _ := s.LastInProgress(ctx, 1); ok {
		t.Error("the series page should agree there is nothing to resume")
	}
}

// Picking Rewatching puts the show on the rail at once, offering episode 1, before any episode is marked.
func TestContinueWatchingOffersAFreshRewatch(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	all := Paging{Page: 1, PerPage: 50}
	seedCatalogue(t, s, 1, "FINISHED", 12)

	for _, ep := range []string{"1", "2", "3"} {
		playThrough(t, s, 1, ep, 0, 1400, 1400)
	}
	if err := s.SetListStatus(ctx, 1, "COMPLETED", -1); err != nil {
		t.Fatal(err)
	}
	if err := s.SetListStatus(ctx, 1, StatusRepeating, -1); err != nil {
		t.Fatal(err)
	}

	page, err := s.ContinueWatching(ctx, all)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Progress != 0 {
		t.Fatalf("a fresh rewatch should offer episode 1: %+v", page.Items)
	}
}
