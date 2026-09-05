package store

import (
	"context"
	"testing"
)

func hasFollow(list []Follow, animeID int) bool {
	for _, f := range list {
		if f.AnimeID == animeID {
			return true
		}
	}
	return false
}

// Picking a show for auto-download is a separate choice from following it, so
// the watcher has to look at it even when it was never followed.
func TestAutoDownloadShowsAreWatched(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedAnime(t, s, 1)
	seedAnime(t, s, 2)

	if err := s.SetAnimePref(ctx, 2, "autodownload.enabled", "true"); err != nil {
		t.Fatal(err)
	}

	follows, err := s.Follows(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if hasFollow(follows, 1) {
		t.Error("an unfollowed show with no auto-download is being watched")
	}
	if !hasFollow(follows, 2) {
		t.Fatal("a show picked for auto-download is not being watched")
	}
}

func TestAutoDownloadIsNotDuplicatedForAFollowedShow(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedAnime(t, s, 1)

	if err := s.SetFollow(ctx, Follow{AnimeID: 1}, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAnimePref(ctx, 1, "autodownload.enabled", "true"); err != nil {
		t.Fatal(err)
	}

	follows, err := s.Follows(ctx)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, f := range follows {
		if f.AnimeID == 1 {
			seen++
		}
	}
	if seen != 1 {
		t.Fatalf("show listed %d times, want once", seen)
	}
}

// The per-anime value overlays the global one, which is what lets a single
// show opt in while everything else stays off.
func TestPerAnimeAutoDownloadOverridesTheGlobal(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedAnime(t, s, 1)
	seedAnime(t, s, 2)

	if err := s.SetSetting(ctx, "autodownload.enabled", "false"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAnimePref(ctx, 1, "autodownload.enabled", "true"); err != nil {
		t.Fatal(err)
	}

	on, err := s.Prefs(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !on.Bool("autodownload.enabled") {
		t.Error("the show that opted in reads as off")
	}

	off, err := s.Prefs(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if off.Bool("autodownload.enabled") {
		t.Error("a show that did not opt in reads as on")
	}
}
