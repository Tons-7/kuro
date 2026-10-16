package store

import (
	"context"
	"testing"
)

func listEntryRow(t *testing.T, s *Store, animeID int) (id, progress int, status string, dirty int) {
	t.Helper()
	err := s.r.QueryRow(
		`SELECT id, progress, coalesce(status,''), dirty FROM list_entry WHERE anime_id = ?`,
		animeID).Scan(&id, &progress, &status, &dirty)
	if err != nil {
		t.Fatal(err)
	}
	return
}

func current() *string { s := "CURRENT"; return &s }

// A local row (negative id) for an anime later imported takes AniList's id instead of aborting the sync.
func TestImportReconcilesALocalEntryOntoItsRealID(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedAnime(t, s, 42)

	// Watched locally, unsynced: negative id, dirty.
	if _, err := s.MarkWatched(ctx, 42, 3); err != nil {
		t.Fatal(err)
	}
	if id, _, _, dirty := listEntryRow(t, s, 42); id >= 0 || dirty != 1 {
		t.Fatalf("local entry id=%d dirty=%d, want negative and dirty", id, dirty)
	}

	// A merge import must not lose the unpushed edit, and must not fail.
	remote := []Entry{{ID: 9001, AnimeID: 42, Status: current(), Progress: 1}}
	if _, err := s.ImportList(ctx, []Anime{{ID: 42, Romaji: "Show", Synonyms: "[]", Genres: "[]"}}, remote, ImportMerge); err != nil {
		t.Fatalf("merge import: %v", err)
	}
	if _, progress, _, dirty := listEntryRow(t, s, 42); progress != 3 || dirty != 1 {
		t.Errorf("after merge: progress=%d dirty=%d, want the local 3 kept dirty", progress, dirty)
	}

	// A replace import overwrites the local edit and adopts the real id.
	if _, err := s.ImportList(ctx, []Anime{{ID: 42, Romaji: "Show", Synonyms: "[]", Genres: "[]"}}, remote, ImportReplace); err != nil {
		t.Fatalf("replace import: %v", err)
	}
	if id, progress, _, dirty := listEntryRow(t, s, 42); id != 9001 || progress != 1 || dirty != 0 {
		t.Errorf("after replace: id=%d progress=%d dirty=%d, want 9001/1/0", id, progress, dirty)
	}
}

// AniList returns an entry per custom list, all one MediaList id, which still dedupes.
func TestImportDedupesRepeatedListEntries(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	rows := []Entry{
		{ID: 500, AnimeID: 7, Status: current(), Progress: 4},
		{ID: 500, AnimeID: 7, Status: current(), Progress: 4},
	}
	anime := []Anime{{ID: 7, Romaji: "Show", Synonyms: "[]", Genres: "[]"}}
	res, err := s.ImportList(ctx, anime, rows, ImportReplace)
	if err != nil {
		t.Fatalf("import with duplicate references: %v", err)
	}
	if res.Entries != 2 {
		t.Errorf("reported %d entries", res.Entries)
	}
	if id, progress, _, _ := listEntryRow(t, s, 7); id != 500 || progress != 4 {
		t.Errorf("deduped row id=%d progress=%d, want 500/4", id, progress)
	}
}

// A replace import mirrors the AniList list, which can never hold a MAL-only show: those entries stay.
func TestReplaceImportKeepsMALOnlyEntries(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.SaveCorpus(ctx, []CorpusEntry{{AniListID: -777, MalID: 777, Kind: "TV", Episodes: 13,
		Titles: []CorpusTitle{{Text: "MAL only", Kind: "primary"}}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyRemote(ctx, RemoteEntry{AnimeID: -777, Status: "CURRENT", Progress: 3}); err != nil {
		t.Fatal(err)
	}
	s.ImportList(ctx, []Anime{{ID: 8, Romaji: "Gone", Synonyms: "[]", Genres: "[]"}},
		[]Entry{{ID: 501, AnimeID: 8, Status: current(), Progress: 1}}, ImportMerge)

	res, err := s.ImportList(ctx, []Anime{{ID: 7, Romaji: "Show", Synonyms: "[]", Genres: "[]"}},
		[]Entry{{ID: 500, AnimeID: 7, Status: current(), Progress: 4}}, ImportReplace)
	if err != nil {
		t.Fatal(err)
	}
	if res.Removed != 1 {
		t.Fatalf("removed %d, want only the AniList show that left the list", res.Removed)
	}
	if e := entryOf(t, s, -777); e.Progress != 3 {
		t.Fatalf("MAL-only entry = %+v, want it kept", e)
	}
	if e := entryOf(t, s, 8); e.AnimeID != 0 {
		t.Fatalf("entry 8 = %+v, want it dropped", e)
	}
}
