package library

import (
	"context"
	"os"
	"testing"

	"kuro/internal/anilist"
)

// Opt-in: KURO_LIVE=1 go test ./internal/library -run LiveRelated -v
// Detective Conan is the hard case: two TV entries and dozens of films.
func TestLiveRelatedFindsTheFilms(t *testing.T) {
	if os.Getenv("KURO_LIVE") == "" {
		t.Skip("set KURO_LIVE=1 to query AniList")
	}
	const conan = 235

	st := prefetchStore(t)
	ctx := context.Background()
	rel := NewRelations(st, anilist.New(discard()), discard())

	if _, err := rel.Fetch(ctx, conan); err != nil {
		t.Fatal(err)
	}

	franchise, err := st.Franchise(ctx, conan)
	if err != nil {
		t.Fatal(err)
	}
	related, err := st.Related(ctx, conan)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d seasons, %d related", len(franchise.Seasons), len(related))

	if len(related) < 10 {
		t.Errorf("found %d related entries; Conan has dozens of films", len(related))
	}
	for _, s := range franchise.Seasons {
		for _, e := range related {
			if e.ID == s.ID {
				t.Errorf("%d is both a season and related", e.ID)
			}
		}
	}
	for _, e := range related {
		if e.Kind == "" {
			t.Fatalf("%d came back with no kind", e.ID)
		}
	}

	// The walk stores bare ids; the page fills the titles, as the handler does.
	var blank []int
	for _, e := range related {
		if e.Romaji == "" {
			blank = append(blank, e.ID)
		}
	}
	if len(blank) > 20 {
		blank = blank[:20]
	}
	if _, err := NewImporter(st, anilist.New(discard()), discard()).Hydrate(ctx, blank); err != nil {
		t.Fatal(err)
	}

	related, err = st.Related(ctx, conan)
	if err != nil {
		t.Fatal(err)
	}
	var named int
	for _, e := range related {
		if e.Romaji != "" {
			named++
		}
	}
	if named < len(blank) {
		t.Errorf("only %d of %d hydrated entries have titles", named, len(blank))
	}
	for _, e := range related[:min(5, len(related))] {
		t.Logf("%s — %s (%v)", e.Kind, e.Romaji, e.Format)
	}
}
