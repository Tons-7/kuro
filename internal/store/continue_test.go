package store

import (
	"context"
	"testing"
)

func started(t *testing.T, s *Store, animeID int, epKey string, at int64) {
	t.Helper()
	if _, err := s.w.ExecContext(context.Background(), `
		INSERT INTO playback (anime_id, ep_key, position_s, duration_s, watched, dismissed, last_played_at)
		VALUES (?,?,120,1440,0,0,?)`, animeID, epKey, at); err != nil {
		t.Fatal(err)
	}
}

// Dismissing an episode must remove that episode from Continue watching, even
// when another started episode keeps the show on the row.
func TestDismissRemovesTheEpisodeOnShow(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedCatalogue(t, s, 185874, "RELEASING", 8)

	started(t, s, 185874, "41", 1000)
	started(t, s, 185874, "44", 2000)

	page, err := s.ContinueWatching(ctx, Paging{PerPage: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Resume == nil {
		t.Fatalf("expected one resumable show, got %d", len(page.Items))
	}
	if got := page.Items[0].Resume.EpKey; got != "44" {
		t.Fatalf("showing episode %q, want the most recent (44)", got)
	}

	if err := s.DismissResume(ctx, 185874, "44"); err != nil {
		t.Fatal(err)
	}

	page, err = s.ContinueWatching(ctx, Paging{PerPage: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("the earlier episode is still unfinished, so the show should stay: got %d", len(page.Items))
	}
	if got := page.Items[0].Resume.EpKey; got != "41" {
		t.Errorf("still showing episode %q after dismissing 44, want 41", got)
	}
}

// Dismissing the only started episode takes the show off the row entirely.
func TestDismissingTheOnlyEpisodeRemovesTheShow(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedCatalogue(t, s, 999, "FINISHED", 12)
	started(t, s, 999, "3", 1000)

	if err := s.DismissResume(ctx, 999, "3"); err != nil {
		t.Fatal(err)
	}

	page, err := s.ContinueWatching(ctx, Paging{PerPage: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 0 {
		t.Errorf("got %d shows, want none", len(page.Items))
	}
}
