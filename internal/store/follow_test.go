package store

import (
	"context"
	"testing"
)

// The same episode is subbed by a dozen groups. Announcing each one is the
// same news repeated, which is what made notifications unusable.
func TestNotifiedOncePerEpisode(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedAnime(t, s, 1)

	if err := s.RecordNotified(ctx, "hash-subsplease", 1, 7); err != nil {
		t.Fatal(err)
	}

	seen, _ := s.AlreadyNotified(ctx, "hash-erai", 1, 7)
	if !seen {
		t.Error("a second group's release of episode 7 announced it again")
	}

	next, _ := s.AlreadyNotified(ctx, "hash-other", 1, 8)
	if next {
		t.Error("episode 8 was suppressed by episode 7")
	}

	other, _ := s.AlreadyNotified(ctx, "hash-other", 2, 7)
	if other {
		t.Error("another show's episode 7 was suppressed")
	}
}

func TestUnknownEpisodeStillDedupesByHash(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedAnime(t, s, 1)

	if err := s.RecordNotified(ctx, "hash-a", 1, 7); err != nil {
		t.Fatal(err)
	}
	if seen, _ := s.AlreadyNotified(ctx, "hash-a", 0, 0); !seen {
		t.Error("the same release was announced twice")
	}
}

// Planning to watch something is not asking to hear about every episode of it.
func TestFollowIsOnlyForShowsBeingWatched(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedAnime(t, s, 1)

	if err := s.SetFollow(ctx, Follow{AnimeID: 1}, true); err != nil {
		t.Fatal(err)
	}
	if !hasFollow(mustFollows(t, s), 1) {
		t.Fatal("following did not take")
	}

	if err := s.SetFollow(ctx, Follow{AnimeID: 1}, false); err != nil {
		t.Fatal(err)
	}
	if hasFollow(mustFollows(t, s), 1) {
		t.Fatal("unfollowing left the show subscribed")
	}
}

func mustFollows(t *testing.T, s *Store) []Follow {
	t.Helper()
	out, err := s.Follows(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return out
}
