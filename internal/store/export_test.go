package store

import (
	"context"
	"testing"
)

func strptr(s string) *string { return &s }

func exportStore(t *testing.T) *Store {
	t.Helper()
	st := newTestStore(t)
	ctx := context.Background()

	episodes := 28
	malID := 52991
	if _, err := st.ImportList(ctx, []Anime{
		{ID: 100, MalID: &malID, Romaji: "Sousou no Frieren", English: strptr("Frieren"),
			Synonyms: "[]", Genres: "[]", Episodes: &episodes},
		{ID: 200, Romaji: "Monster", Synonyms: "[]", Genres: "[]"},
		{ID: 300, Romaji: "Never Touched", Synonyms: "[]", Genres: "[]"},
	}, nil, ImportMerge); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestExportCoversWatchedAndFavourited(t *testing.T) {
	st := exportStore(t)
	ctx := context.Background()

	if _, err := st.MarkWatched(ctx, 100, 9); err != nil {
		t.Fatal(err)
	}
	if err := st.SetScore(ctx, 100, 85); err != nil {
		t.Fatal(err)
	}
	if err := st.SetBookmark(ctx, 200, Bookmark{Favourite: true, Note: "start it"}); err != nil {
		t.Fatal(err)
	}

	got, err := st.ExportLibrary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("exported %d entries, want the watched one and the favourite: %+v", len(got), got)
	}

	byID := map[int]ExportEntry{}
	for _, e := range got {
		byID[e.AnimeID] = e
	}

	frieren := byID[100]
	if frieren.Title != "Sousou no Frieren" || frieren.English != "Frieren" {
		t.Errorf("titles = %+v", frieren)
	}
	if frieren.Progress != 9 || frieren.Score != 85 || frieren.Status != "CURRENT" {
		t.Errorf("entry = %+v", frieren)
	}
	if frieren.MalID == nil || *frieren.MalID != 52991 {
		t.Errorf("mal id missing: %+v", frieren.MalID)
	}
	if frieren.Episodes == nil || *frieren.Episodes != 28 {
		t.Errorf("episode count missing: %+v", frieren.Episodes)
	}

	monster := byID[200]
	if !monster.Favourite || monster.Note != "start it" {
		t.Errorf("favourite = %+v", monster)
	}
	if _, ok := byID[300]; ok {
		t.Error("an untouched show was exported")
	}
}

func TestImportRestoresIntoAnEmptyLibrary(t *testing.T) {
	st := exportStore(t)
	ctx := context.Background()

	rep, err := st.ImportEntries(ctx, []ExportEntry{
		{AnimeID: 100, Title: "Sousou no Frieren", Status: "COMPLETED", Progress: 28,
			Score: 90, Repeat: 1, Favourite: true},
		{AnimeID: 200, Title: "Monster", Status: "PLANNING"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Entries != 2 || rep.Favourites != 1 {
		t.Fatalf("report = %+v", rep)
	}

	e, err := st.ListEntry(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	if e.Progress != 28 || e.Score != 90 || e.Repeat != 1 || e.Status != "COMPLETED" {
		t.Fatalf("entry = %+v", e)
	}
	if b, _ := st.Bookmark(ctx, 100); !b.Favourite {
		t.Error("the favourite was not restored")
	}

	// Imported rows are local edits, so they reach the trackers.
	dirty, err := st.DirtyEntries(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirty) != 2 {
		t.Fatalf("dirty = %+v, want both queued for push", dirty)
	}
}

// An import is a merge, not a reset: a file older than what is here must not
// wind progress back.
func TestImportNeverMovesProgressBackwards(t *testing.T) {
	st := exportStore(t)
	ctx := context.Background()

	if _, err := st.MarkWatched(ctx, 100, 20); err != nil {
		t.Fatal(err)
	}
	if err := st.SetScore(ctx, 100, 70); err != nil {
		t.Fatal(err)
	}

	if _, err := st.ImportEntries(ctx, []ExportEntry{
		{AnimeID: 100, Progress: 5, Status: "CURRENT"},
	}); err != nil {
		t.Fatal(err)
	}

	e, _ := st.ListEntry(ctx, 100)
	if e.Progress != 20 {
		t.Errorf("progress = %d, want the further watched count kept", e.Progress)
	}
	if e.Score != 70 {
		t.Errorf("score = %d, want the existing rating kept when the file has none", e.Score)
	}
}

func TestImportSkipsUnusableRows(t *testing.T) {
	st := exportStore(t)

	rep, err := st.ImportEntries(context.Background(), []ExportEntry{
		{AnimeID: 0, Title: "no id"},
		{AnimeID: -5, Title: "negative"},
		{AnimeID: 100, Progress: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Entries != 1 || rep.Skipped != 2 {
		t.Fatalf("report = %+v", rep)
	}
}

// An id the corpus has never seen still has to import: the placeholder row is
// what keeps the foreign key.
func TestImportAcceptsUnknownAnime(t *testing.T) {
	st := exportStore(t)
	ctx := context.Background()

	if _, err := st.ImportEntries(ctx, []ExportEntry{
		{AnimeID: 999999, Title: "Something Else", Progress: 3},
	}); err != nil {
		t.Fatal(err)
	}
	if e, _ := st.ListEntry(ctx, 999999); e.Progress != 3 {
		t.Fatalf("entry = %+v", e)
	}
}

func TestExportImportRoundTrip(t *testing.T) {
	st := exportStore(t)
	ctx := context.Background()

	st.MarkWatched(ctx, 100, 12)
	st.SetScore(ctx, 100, 75)
	st.SetBookmark(ctx, 200, Bookmark{Favourite: true})

	saved, err := st.ExportLibrary(ctx)
	if err != nil {
		t.Fatal(err)
	}

	fresh := exportStore(t)
	if _, err := fresh.ImportEntries(ctx, saved); err != nil {
		t.Fatal(err)
	}

	got, err := fresh.ExportLibrary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(saved) {
		t.Fatalf("round trip returned %d entries, want %d", len(got), len(saved))
	}
	for i := range saved {
		if got[i].AnimeID != saved[i].AnimeID || got[i].Progress != saved[i].Progress ||
			got[i].Score != saved[i].Score || got[i].Status != saved[i].Status ||
			got[i].Favourite != saved[i].Favourite {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], saved[i])
		}
	}
}
