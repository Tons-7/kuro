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

// The friend's crash: an episode marked watched before the anime was on the
// AniList list creates a local row with a synthetic negative id keyed on
// anime_id. Importing that anime then brings AniList's own id, which a plain
// ON CONFLICT(id) misses — the insert used to hit the anime_id UNIQUE
// constraint and abort the whole sync.
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

// AniList returns one entry per custom list the anime belongs to, all sharing
// one MediaList id. Deduping on the id must still work.
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
