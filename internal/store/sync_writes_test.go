package store

import (
	"context"
	"slices"
	"testing"
	"time"
)

// A show AniList no longer returns is marked dead; asking for it again every run would waste a request.
func TestStaleAiringSkipsDeadShows(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Now().Unix()

	for _, id := range []int{1, 2} {
		if _, err := s.w.ExecContext(ctx,
			`INSERT INTO anime (id, title_romaji, status, next_airing_at, synced_at) VALUES (?, 'x', 'RELEASING', ?, ?)`,
			id, now-600, now-3600); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.MarkDead(ctx, []int{2}); err != nil {
		t.Fatal(err)
	}

	ids, err := s.StaleAiring(ctx, time.Now(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ids, []int{1}) {
		t.Fatalf("due = %v, want [1]: the dead show is not asked for again", ids)
	}
}

// Phone keyboards type a curly apostrophe; the stored title has a straight one, and accented Latin is
// folded by the index, so both stay on the FTS path.
func TestSearchLocalMatchesCurlyQuotesAndAccents(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if _, err := s.ImportList(ctx,
		[]Anime{anime(1, "JoJo's Bizarre Adventure"), anime(2, "Pokemon Horizons")}, nil, ImportMerge); err != nil {
		t.Fatal(err)
	}
	for q, want := range map[string]int{"JoJo’s Bizarre": 1, "Pokémon": 2} {
		got, err := s.SearchLocal(ctx, q, 0)
		if err != nil || len(got) != 1 || got[0].ID != want {
			t.Errorf("%q: got %+v (err %v), want show %d", q, got, err, want)
		}
	}
}

// Two-character and CJK queries take LIKE, keeping list shows first and the text literal.
func TestSearchLocalLikePathKeepsListFirstAndLiteral(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	status := "CURRENT"
	native := "進撃の巨人"
	popular := 900
	shows := []Anime{
		{ID: 1, Romaji: "Mob Psycho 100", Synonyms: "[]", Genres: "[]"},
		{ID: 2, Romaji: "Monster", Synonyms: "[]", Genres: "[]", Popularity: &popular},
		{ID: 3, Romaji: "Shingeki no Kyojin", Native: &native, Synonyms: "[]", Genres: "[]"},
	}
	if _, err := s.ImportList(ctx, shows,
		[]Entry{{ID: 1, AnimeID: 1, Status: &status, UpdatedAt: 1000}}, ImportMerge); err != nil {
		t.Fatal(err)
	}

	got, err := s.SearchLocal(ctx, "mo", 1)
	if err != nil || len(got) != 1 || got[0].ID != 1 {
		t.Fatalf("'mo' = %+v (err %v), want the show on the list before the more popular one", got, err)
	}
	if got, _ := s.SearchLocal(ctx, "進撃", 0); len(got) != 1 || got[0].ID != 3 {
		t.Fatalf("a Japanese title = %+v, want show 3", got)
	}
	for _, q := range []string{"__", "%%"} {
		if got, _ := s.SearchLocal(ctx, q, 0); len(got) != 0 {
			t.Errorf("%q matched %d rows; wildcards are not search terms", q, len(got))
		}
	}
	if page, _ := s.Library(ctx, LibraryFilter{Query: "%"}, Paging{Page: 1, PerPage: 10}); len(page.Items) != 0 {
		t.Errorf("the library filter %q matched %d rows", "%", len(page.Items))
	}
}

// A sync brings every show back unchanged most of the time; a row that still says the same is left
// alone until its stamp is an hour old, and an entry with the same stamp is not rewritten either.
func TestImportListLeavesUnchangedRowsAlone(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	status := "CURRENT"
	entry := Entry{ID: 1, AnimeID: 100, Status: &status, UpdatedAt: 1000}

	if _, err := s.ImportList(ctx, []Anime{anime(100, "Frieren")}, []Entry{entry}, ImportMerge); err != nil {
		t.Fatal(err)
	}
	recent := time.Now().Unix() - 60
	if _, err := s.w.ExecContext(ctx, `UPDATE anime SET synced_at = ?`, recent); err != nil {
		t.Fatal(err)
	}

	synced := func() (at int64) {
		if err := s.r.QueryRowContext(ctx, `SELECT synced_at FROM anime WHERE id = 100`).Scan(&at); err != nil {
			t.Fatal(err)
		}
		return at
	}

	if _, err := s.ImportList(ctx, []Anime{anime(100, "Frieren")}, []Entry{entry}, ImportMerge); err != nil {
		t.Fatal(err)
	}
	if got := synced(); got != recent {
		t.Errorf("synced_at = %d, want %d: an unchanged show was rewritten", got, recent)
	}

	renamed := anime(100, "Frieren: Beyond Journey's End")
	if _, err := s.ImportList(ctx, []Anime{renamed}, nil, ImportMerge); err != nil {
		t.Fatal(err)
	}
	if synced() == recent {
		t.Error("a renamed show kept its old stamp")
	}

	old := time.Now().Unix() - 2*3600
	if _, err := s.w.ExecContext(ctx, `UPDATE anime SET synced_at = ?`, old); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ImportList(ctx, []Anime{renamed}, nil, ImportMerge); err != nil {
		t.Fatal(err)
	}
	if synced() == old {
		t.Error("a stale stamp should be refreshed even when nothing else changed")
	}

	newer := entry
	newer.UpdatedAt, newer.Progress = 2000, 3
	if _, err := s.ImportList(ctx, nil, []Entry{newer}, ImportMerge); err != nil {
		t.Fatal(err)
	}
	var got int
	if err := s.r.QueryRowContext(ctx, `SELECT progress FROM list_entry WHERE id = 1`).Scan(&got); err != nil || got != 3 {
		t.Errorf("progress = %d (err %v), want the newer remote entry applied", got, err)
	}
}
