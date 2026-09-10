package store

import (
	"context"
	"testing"

	"kuro/internal/score"
)

// A feature-length episode is a bigger file at the same quality, so the size
// cap has to know how long the thing is. The runtime comes from the catalogue.
func TestEpisodeRuntimeFeedsTheSizeCap(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	movie, tv := 117, 24
	if _, err := s.ImportList(ctx, []Anime{
		{ID: 1, Romaji: "A Film", Duration: &movie},
		{ID: 2, Romaji: "A Series", Duration: &tv},
		{ID: 3, Romaji: "Unknown Length"},
	}, nil, ImportMerge); err != nil {
		t.Fatal(err)
	}

	if got := s.EpisodeRuntime(ctx, 1); got != 117 {
		t.Errorf("film runtime = %d, want 117", got)
	}
	if got := s.EpisodeRuntime(ctx, 2); got != 24 {
		t.Errorf("series runtime = %d, want 24", got)
	}
	if got := s.EpisodeRuntime(ctx, 3); got != 0 {
		t.Errorf("unknown runtime = %d, want 0", got)
	}
	if got := s.EpisodeRuntime(ctx, 999); got != 0 {
		t.Errorf("missing show runtime = %d, want 0", got)
	}

	// The point of carrying it: a 3 GiB cap does not reject the film.
	const cap3GiB = 3 << 30
	prefs := score.Preferences{MaxAutoBytes: cap3GiB}
	film := score.Candidate{RuntimeMinutes: s.EpisodeRuntime(ctx, 1)}
	series := score.Candidate{RuntimeMinutes: s.EpisodeRuntime(ctx, 2)}
	if film.SizeLimit(prefs) <= series.SizeLimit(prefs) {
		t.Errorf("film cap %d is no larger than the episode cap %d",
			film.SizeLimit(prefs), series.SizeLimit(prefs))
	}
	if series.SizeLimit(prefs) != cap3GiB {
		t.Errorf("a normal episode moved the cap: %d", series.SizeLimit(prefs))
	}
}
