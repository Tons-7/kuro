package store

import (
	"context"
	"testing"

	"kuro/internal/match"
)

func corpusEntry(id int, titles ...string) CorpusEntry {
	e := CorpusEntry{AniListID: id, Kind: "TV", Episodes: 12, Year: 2023}
	for i, t := range titles {
		kind := "synonym"
		if i == 0 {
			kind = "primary"
		}
		e.Titles = append(e.Titles, CorpusTitle{Text: t, Kind: kind})
	}
	return e
}

func TestSaveCorpusStoresTitles(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	written, err := s.SaveCorpus(ctx, []CorpusEntry{
		corpusEntry(154587, "Sousou no Frieren", "Frieren: Beyond Journey's End", "葬送のフリーレン"),
		corpusEntry(16498, "Shingeki no Kyojin", "Attack on Titan", "SnK"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if written != 6 {
		t.Fatalf("wrote %d titles, want 6", written)
	}

	stats, err := s.CorpusStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Anime != 2 || stats.Titles != 6 {
		t.Fatalf("stats = %+v", stats)
	}
}

// Re-running the seed must not multiply rows, since it is a periodic job.
func TestSaveCorpusIsIdempotent(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	entries := []CorpusEntry{corpusEntry(1, "Show", "Show Alternative")}
	for range 3 {
		if _, err := s.SaveCorpus(ctx, entries); err != nil {
			t.Fatal(err)
		}
	}

	stats, _ := s.CorpusStats(ctx)
	if stats.Anime != 1 || stats.Titles != 2 {
		t.Fatalf("stats = %+v after three runs", stats)
	}
}

// A later source may know a field the seed did not; it must not blank one it
// does not know about.
func TestSaveCorpusMergesFields(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s.SaveCorpus(ctx, []CorpusEntry{{
		AniListID: 1, MalID: 100, Kind: "TV", Episodes: 12,
		Titles: []CorpusTitle{{Text: "Show", Kind: "primary"}},
	}})
	s.SaveCorpus(ctx, []CorpusEntry{{
		AniListID: 1, AniDBID: 200, Year: 2023,
		Titles: []CorpusTitle{{Text: "Show Second Name", Kind: "synonym"}},
	}})

	var mal, anidb, year int
	err := s.r.QueryRowContext(ctx,
		`SELECT mal_id, anidb_id, year FROM corpus_anime WHERE anime_id = 1`).Scan(&mal, &anidb, &year)
	if err != nil {
		t.Fatal(err)
	}
	if mal != 100 {
		t.Errorf("mal_id = %d; the second write erased it", mal)
	}
	if anidb != 200 || year != 2023 {
		t.Errorf("anidb=%d year=%d; new fields not merged", anidb, year)
	}
}

func TestSaveCorpusSkipsUnusableRows(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	written, err := s.SaveCorpus(ctx, []CorpusEntry{
		{AniListID: 0, Titles: []CorpusTitle{{Text: "No id"}}},
		{AniListID: 5, Titles: []CorpusTitle{{Text: ""}, {Text: "  "}, {Text: "!!!"}, {Text: "Real"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Empty, blank and punctuation-only titles normalise to nothing.
	if written != 1 {
		t.Fatalf("wrote %d titles, want only the usable one", written)
	}

	stats, _ := s.CorpusStats(ctx)
	if stats.Anime != 1 {
		t.Fatalf("anime = %d; the entry with no id should be skipped", stats.Anime)
	}
}

func TestKnownIDsIncludesDead(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s.SaveCorpus(ctx, []CorpusEntry{corpusEntry(1, "A"), corpusEntry(2, "B")})
	if err := s.MarkDead(ctx, []int{99, 100}); err != nil {
		t.Fatal(err)
	}

	known, err := s.KnownIDs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Dead ids count as known, or every refresh re-requests them forever.
	for _, id := range []int{1, 2, 99, 100} {
		if _, ok := known[id]; !ok {
			t.Errorf("id %d missing from known set", id)
		}
	}
	if len(known) != 4 {
		t.Fatalf("known = %d entries", len(known))
	}
}

func TestMarkDeadIsIdempotent(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	for range 3 {
		if err := s.MarkDead(ctx, []int{7}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.MarkDead(ctx, nil); err != nil {
		t.Fatal(err)
	}

	stats, _ := s.CorpusStats(ctx)
	if stats.Dead != 1 {
		t.Fatalf("dead = %d, want 1", stats.Dead)
	}
}

func TestSourceRefreshTracking(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	at, err := s.SourceRefreshedAt(ctx, "manami")
	if err != nil {
		t.Fatal(err)
	}
	if !at.IsZero() {
		t.Fatal("an unseen source should report the zero time")
	}

	if err := s.MarkSource(ctx, "manami", 41537); err != nil {
		t.Fatal(err)
	}
	if at, _ = s.SourceRefreshedAt(ctx, "manami"); at.IsZero() {
		t.Fatal("refresh time not recorded")
	}
}

func TestBuildIndexResolvesEveryTitleVariant(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s.SaveCorpus(ctx, []CorpusEntry{{
		AniListID: 154587, Kind: "TV", Episodes: 28, Year: 2023,
		Titles: []CorpusTitle{
			{Text: "Frieren: Beyond Journey's End", Kind: "official"},
			{Text: "Sousou no Frieren", Kind: "primary"},
			{Text: "葬送のフリーレン", Kind: "native"},
		},
	}})

	ix, err := s.BuildIndex(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if ix.Len() != 1 {
		t.Fatalf("indexed %d entries", ix.Len())
	}

	for _, q := range []string{
		"Sousou no Frieren",
		"Frieren Beyond Journeys End",
		"葬送のフリーレン",
	} {
		got, ok := ix.Match(match.Query{Title: q})
		if !ok || got.MediaID != 154587 {
			t.Errorf("%q matched %+v", q, got)
		}
	}
}

// Unknown year and episode count are stored as NULL, and one such row must not
// fail the whole index build.
func TestBuildIndexToleratesNullMetadata(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s.SaveCorpus(ctx, []CorpusEntry{
		{AniListID: 1, Titles: []CorpusTitle{{Text: "Unknown Year Show", Kind: "primary"}}},
		corpusEntry(2, "Fully Populated Show"),
	})

	ix, err := s.BuildIndex(ctx)
	if err != nil {
		t.Fatalf("null metadata failed the build: %v", err)
	}
	if ix.Len() != 2 {
		t.Fatalf("indexed %d entries, want 2", ix.Len())
	}
	if got, ok := ix.Match(match.Query{Title: "Unknown Year Show"}); !ok || got.MediaID != 1 {
		t.Fatalf("the null-metadata entry is not matchable: %+v", got)
	}
}

func TestBuildIndexEmptyCorpus(t *testing.T) {
	s := newTestStore(t)

	ix, err := s.BuildIndex(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ix.Len() != 0 {
		t.Fatalf("indexed %d entries from an empty corpus", ix.Len())
	}
}
