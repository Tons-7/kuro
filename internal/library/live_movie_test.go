package library

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"kuro/internal/score"
	"kuro/internal/store"
)

// A feature is one long episode: against the per-episode cap every release of
// it was rejected. Opt-in: KURO_LIVE=1 go test ./internal/library -run LiveMovie -v
func TestLiveMovieIsNotRejectedForItsSize(t *testing.T) {
	if os.Getenv("KURO_LIVE") == "" {
		t.Skip("set KURO_LIVE=1 to search the live indexers")
	}

	films := []struct {
		id      int
		romaji  string
		english *string
		minutes int
	}{
		{3783, "Kara no Kyoukai: Tsuukaku Zanryuu", str("the Garden of sinners Chapter 3: ever cry, never life."), 58},
		{21519, "Kimi no Na wa.", str("Your Name."), 106},
	}

	st := prefetchStore(t)
	ctx := context.Background()
	for _, f := range films {
		eps, mins, format := 1, f.minutes, "MOVIE"
		if _, err := st.ImportList(ctx, []store.Anime{{
			ID: f.id, Romaji: f.romaji, English: f.english, Synonyms: `[]`,
			Genres: "[]", Episodes: &eps, Duration: &mins, Format: &format,
		}}, nil, store.ImportMerge); err != nil {
			t.Fatal(err)
		}
		if got := st.EpisodeRuntime(ctx, f.id); got != f.minutes {
			t.Fatalf("%s runtime = %d, want %d", f.romaji, got, f.minutes)
		}
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	finder := NewFinder(st, liveSources(t), log)
	prefs := score.DefaultPreferences()

	for _, f := range films {
		t.Run(f.romaji, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
			defer cancel()
			got, err := finder.Find(ctx, Request{AnimeID: f.id, Episode: 1, Season: 1, Prefs: prefs})
			if err != nil {
				t.Fatal(err)
			}
			if got.Best == nil {
				t.Fatalf("nothing pickable among %d results for a %d-minute film", len(got.Results), f.minutes)
			}

			// What the fix bought: releases the flat cap would have thrown out.
			var rescued int
			for _, r := range got.Results {
				if r.Blocked != "" {
					continue
				}
				if r.EpisodeBytes() > prefs.MaxAutoBytes {
					rescued++
				}
			}
			t.Logf("%s -> %s (%.2f GiB, %d seeders); %d/%d over the flat cap",
				f.romaji, got.Best.Torrent.Title,
				float64(got.Best.Candidate.EpisodeBytes())/(1<<30),
				got.Best.Torrent.Seeders, rescued, len(got.Results))
		})
	}
}
