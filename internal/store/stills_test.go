package store

import (
	"context"
	"testing"
	"time"

	"kuro/internal/metadata"
)

func seedCatalogue(t *testing.T, s *Store, id int, status string, count int) {
	t.Helper()
	if _, err := s.w.ExecContext(context.Background(),
		`INSERT INTO anime (id, title_romaji, status, episode_count, synced_at)
		 VALUES (?,?,?,?,unixepoch())`,
		id, "Show", status, count); err != nil {
		t.Fatal(err)
	}
}

// The original rule was "stale only if there are no rows at all", which meant a
// show first opened at episode three never learned about episode four.
func TestEpisodesStaleWhileAiring(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedCatalogue(t, s, 1, "RELEASING", 12)

	if _, err := s.SaveEpisodes(ctx, 1, []metadata.Episode{
		{Number: 1, TitleEN: "One", Image: "a.jpg"},
		{Number: 2, TitleEN: "Two", Image: "b.jpg"},
	}); err != nil {
		t.Fatal(err)
	}

	if !s.EpisodesStale(ctx, 1, time.Hour) {
		t.Fatal("a show still airing with two of twelve episodes is not stale")
	}
	if err := s.MarkEpisodesFetched(ctx, 1, 2); err != nil {
		t.Fatal(err)
	}
	if s.EpisodesStale(ctx, 1, time.Hour) {
		t.Error("refetched moments ago, should not be stale again")
	}
	if !s.EpisodesStale(ctx, 1, time.Nanosecond) {
		t.Error("past its refresh window, should be stale")
	}
}

func TestEpisodesStaleFinishedAndComplete(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedCatalogue(t, s, 2, "FINISHED", 2)

	if _, err := s.SaveEpisodes(ctx, 2, []metadata.Episode{
		{Number: 1, TitleEN: "One", Image: "a.jpg"},
		{Number: 2, TitleEN: "Two", Image: "b.jpg"},
	}); err != nil {
		t.Fatal(err)
	}

	// Nothing left to learn: never fetched again, whatever the age.
	if s.EpisodesStale(ctx, 2, time.Nanosecond) {
		t.Error("a finished show with every title and still should never refetch")
	}
}

func TestStillGapsAndFill(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedCatalogue(t, s, 3, "RELEASING", 8)

	if _, err := s.SaveEpisodes(ctx, 3, []metadata.Episode{
		{Number: 1, TitleEN: "God of Thunder", Image: "have.jpg"},
		{Number: 2, TitleEN: "Son of Darkness"},
	}); err != nil {
		t.Fatal(err)
	}

	missing, err := s.StillGaps(ctx, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 1 || !missing[2] {
		t.Fatalf("missing = %v, want just 2", missing)
	}

	n, err := s.SetEpisodeStills(ctx, 3, map[int]string{1: "no.jpg", 2: "yes.jpg"})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("filled %d, want 1: an existing still must not be overwritten", n)
	}

	eps, err := s.Episodes(ctx, 3)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range eps {
		want := map[int]string{1: "have.jpg", 2: "yes.jpg"}[e.Number]
		if e.Still == nil || *e.Still != want {
			t.Errorf("episode %d still = %v, want %q", e.Number, e.Still, want)
		}
	}
}

// Rows invented from an episode count are offered as something to play, but
// they are not evidence the episode exists and must say so.
func TestPlannedEpisodesAreMarked(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedCatalogue(t, s, 4, "FINISHED", 3)

	eps, err := s.Episodes(ctx, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(eps) != 3 {
		t.Fatalf("got %d episodes, want 3", len(eps))
	}
	for _, e := range eps {
		if !e.Planned {
			t.Errorf("episode %d is invented but not marked planned", e.Number)
		}
	}
}
