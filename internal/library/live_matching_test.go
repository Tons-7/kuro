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

// Titles most exposed to the one-word and sequel problems, against the live
// indexers. Opt-in: KURO_LIVE=1 go test ./internal/library -run Live.
func TestLiveMatchingOnExposedTitles(t *testing.T) {
	if os.Getenv("KURO_LIVE") == "" {
		t.Skip("set KURO_LIVE=1 to search the live indexers")
	}

	shows := []struct {
		id       int
		romaji   string
		english  *string
		synonyms string
		episodes int
		episode  int
	}{
		{19, "MONSTER", nil, `["Monster"]`, 74, 30},
		{21, "ONE PIECE", nil, `[]`, 0, 1100},
		{112, "AIR", nil, `[]`, 12, 3},
		{21995, "Orange", nil, `[]`, 13, 4},
		{104207, "Given", nil, `[]`, 11, 5},
		{20826, "Free!", nil, `[]`, 12, 2},
		{21234, "Boku dake ga Inai Machi", str("ERASED"), `[]`, 12, 6},
		{3701, "Kaiba", nil, `[]`, 12, 2},
		{114745, "Made in Abyss: Retsujitsu no Ougonkyou", str("Made in Abyss: The Golden City of the Scorching Sun"), `[]`, 12, 3},
		{146065, "Mushoku Tensei II: Isekai Ittara Honki Dasu", str("Mushoku Tensei: Jobless Reincarnation Season 2"), `[]`, 12, 5},
	}

	st := prefetchStore(t)
	ctx := context.Background()
	for _, s := range shows {
		eps := s.episodes
		if _, err := st.ImportList(ctx, []store.Anime{{
			ID: s.id, Romaji: s.romaji, English: s.english, Synonyms: s.synonyms,
			Genres: "[]", Episodes: &eps,
		}}, nil, store.ImportMerge); err != nil {
			t.Fatal(err)
		}
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	f := NewFinder(st, liveSources(t), log)

	for _, s := range shows {
		t.Run(s.romaji, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
			defer cancel()
			got, err := f.Find(ctx, Request{
				AnimeID: s.id, Episode: s.episode, Season: 1, Prefs: score.DefaultPreferences(),
			})
			if err != nil {
				t.Fatal(err)
			}
			if got.Best == nil {
				t.Fatalf("nothing pickable among %d results", len(got.Results))
			}
			known := []string{s.romaji}
			if s.english != nil {
				known = append(known, *s.english)
			}
			if !identifiesShow(got.Best.Release, got.Best.Torrent.Title, known) {
				t.Errorf("picked %q, which does not name the show", got.Best.Torrent.Title)
			}
			t.Logf("%s -> %s (%d seeders)", s.romaji, got.Best.Torrent.Title, got.Best.Torrent.Seeders)
		})
	}
}

func str(s string) *string { return &s }
