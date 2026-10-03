package library

import (
	"context"
	"slices"
	"testing"

	"kuro/internal/indexer"
	"kuro/internal/store"
)

// A fresh install has no titles until its first corpus build; a download
// queued then must fetch the show rather than fail with "no release found".
func TestFindFetchesAShowItDoesNotKnow(t *testing.T) {
	st := prefetchStore(t)
	ctx := context.Background()
	var asked []int
	finder := NewFinder(st, fixedIndexer{results: []indexer.Torrent{
		release("aaaa", "[SubsPlease] Sousou no Frieren - 05 (1080p) [ABCD1234].mkv", 50),
	}}, discard()).WithHydrator(func(ctx context.Context, ids []int) (int, error) {
		asked = append(asked, ids...)
		_, err := st.ImportList(ctx, []store.Anime{{ID: 154587, Romaji: "Sousou no Frieren", Synonyms: "[]", Genres: "[]"}},
			nil, store.ImportMerge)
		return 1, err
	})

	got, err := finder.Find(ctx, Request{AnimeID: 154587, Episode: 5, Prefs: defaultScorePrefs()})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(asked, []int{154587}) {
		t.Errorf("fetched %v, want the show", asked)
	}
	if len(got.Queries) == 0 || len(got.Results) == 0 {
		t.Fatalf("nothing found after fetching the show: %+v", got)
	}
}
