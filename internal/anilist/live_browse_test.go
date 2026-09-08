package anilist

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
)

// Opt-in: KURO_LIVE=1 go test ./internal/anilist -run LiveBrowseSort -v
// Proves AniList itself orders search results by the sort we send, rather than
// falling back to relevance.
func TestLiveBrowseSortOrdersSearchResults(t *testing.T) {
	if os.Getenv("KURO_LIVE") == "" {
		t.Skip("set KURO_LIVE=1 to query AniList")
	}
	c := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := context.Background()
	search := Browse{Search: "conan", Formats: []string{"MOVIE"}, PerPage: 10}

	relevance, err := c.BrowseMedia(ctx, search)
	if err != nil {
		t.Fatal(err)
	}
	byScore := search
	byScore.Sort = "score"
	scored, err := c.BrowseMedia(ctx, byScore)
	if err != nil {
		t.Fatal(err)
	}
	if len(scored.Media) < 3 || len(relevance.Media) < 3 {
		t.Fatalf("too few results: %d relevance, %d scored", len(relevance.Media), len(scored.Media))
	}

	score := func(m Media) int {
		if m.AverageScore == nil {
			return 0
		}
		return *m.AverageScore
	}
	for i := 1; i < len(scored.Media); i++ {
		if score(scored.Media[i-1]) < score(scored.Media[i]) {
			t.Errorf("scores are not descending: %d before %d",
				score(scored.Media[i-1]), score(scored.Media[i]))
			break
		}
	}
	if scored.Media[0].ID == relevance.Media[0].ID && scored.Media[1].ID == relevance.Media[1].ID {
		t.Error("sorting by score returned the relevance order unchanged")
	}
}
