package store

import (
	"context"
	"testing"
	"time"
)

func airingAnime(t *testing.T, nextEpisode int, airingAt int64) *Store {
	t.Helper()
	st := newTestStore(t)
	episodes := 13
	next := nextEpisode
	at := int(airingAt)
	if _, err := st.ImportList(context.Background(), []Anime{{
		ID: 7, Romaji: "Show", Synonyms: "[]", Genres: "[]",
		Episodes: &episodes, NextEpisode: &next, NextAiringAt: &at,
	}}, nil, ImportMerge); err != nil {
		t.Fatal(err)
	}
	return st
}

// Auto-next and prefetch ask the same question the watcher does; an episode
// airing next Saturday has nothing to find yet.
func TestNextEpisodeWaitsForTheBroadcast(t *testing.T) {
	ctx := context.Background()

	st := airingAnime(t, 6, time.Now().Add(24*time.Hour).Unix())
	if got, err := st.NextEpisode(ctx, 7, 5); err != nil || got != 0 {
		t.Fatalf("NextEpisode = %d, %v, want nothing until it airs", got, err)
	}
	if got, _ := st.NextEpisode(ctx, 7, 3); got != 4 {
		t.Errorf("NextEpisode(3) = %d, want an episode already out", got)
	}

	st = airingAnime(t, 6, time.Now().Add(-time.Hour).Unix())
	if got, _ := st.NextEpisode(ctx, 7, 5); got != 6 {
		t.Errorf("NextEpisode = %d, want 6 once it has aired", got)
	}

	if err := st.SetAnimePref(ctx, 7, "playback.skip_filler", "true"); err != nil {
		t.Fatal(err)
	}
	st = airingAnime(t, 6, time.Now().Add(24*time.Hour).Unix())
	st.SetAnimePref(ctx, 7, "playback.skip_filler", "true")
	if got, _ := st.NextEpisode(ctx, 7, 5); got != 0 {
		t.Errorf("with filler skipping, NextEpisode = %d, want nothing until it airs", got)
	}
}
