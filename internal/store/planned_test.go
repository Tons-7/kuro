package store

import (
	"context"
	"testing"
)

func animeWithCount(t *testing.T, s *Store, id, episodes int) {
	t.Helper()
	_, err := s.w.Exec(
		`INSERT INTO anime (id, title_romaji, episode_count, synced_at) VALUES (?,?,?,0)
		 ON CONFLICT(id) DO UPDATE SET episode_count = excluded.episode_count`,
		id, "Show", episodes)
	if err != nil {
		t.Fatal(err)
	}
}

// A show the metadata sources have never heard of still has a known episode
// count, and used to render as having no episodes at all.
func TestEpisodesFallBackToTheCount(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	animeWithCount(t, s, 1, 8)

	eps, err := s.Episodes(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(eps) != 8 {
		t.Fatalf("got %d episodes, want 8", len(eps))
	}
	for i, e := range eps {
		want := i + 1
		if e.Number != want || e.Display != want || e.EpKey != itoa(want) {
			t.Fatalf("row %d = %+v", i, e)
		}
	}
}

func TestPlannedEpisodesCarryWatchState(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	animeWithCount(t, s, 1, 4)

	playThrough(t, s, 1, "2", 0, 1400, 1400)

	eps, err := s.Episodes(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !eps[1].Watched {
		t.Error("episode 2 was played to the end but is not marked watched")
	}
	if eps[0].Watched {
		t.Error("episode 1 was never played")
	}
}

func TestNoEpisodesWhenTheCountIsUnknown(t *testing.T) {
	s := newTestStore(t)
	animeWithCount(t, s, 1, 0)

	eps, err := s.Episodes(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(eps) != 0 {
		t.Fatalf("invented %d episodes for a show with no count", len(eps))
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
