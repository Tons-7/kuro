package indexer

import (
	"context"
	"os"
	"testing"
)

// Sukebei serves the same feed under its own XML namespace, so a namespace-
// qualified struct tag silently yields items with no info hash.
// Opt-in: KURO_LIVE=1 KURO_SUKEBEI=<site url> go test ./internal/indexer -run Sukebei
func TestSukebeiReturnsUsableTorrents(t *testing.T) {
	base := os.Getenv("KURO_SUKEBEI")
	if os.Getenv("KURO_LIVE") == "" || base == "" {
		t.Skip("set KURO_LIVE=1 and KURO_SUKEBEI to run against the network")
	}

	got, err := NewSukebei(base).Search(context.Background(), Query{Text: "Someya"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatal("no results; the main trackers carry no adult titles at all")
	}

	for _, r := range got {
		if r.InfoHash == "" {
			t.Fatalf("result has no info hash, so it cannot be played: %q", r.Title)
		}
	}
	t.Logf("%d results, first: %s (%d seeders)", len(got), got[0].Title, got[0].Seeders)
}
