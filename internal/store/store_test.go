package store

import (
	"context"
	"path/filepath"
	"testing"

	"kuro/internal/db"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })

	if err := conn.Migrate(); err != nil {
		t.Fatal(err)
	}
	return New(conn)
}

func TestMigrationsSeedDefaults(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	settings, err := s.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Compared against the compiled-in defaults rather than literals, so a
	// migration that changes a value cannot silently disagree with the code.
	for _, key := range []string{
		"cache.budget_bytes", "quality.max_auto_bytes",
		"sync.progress_at", "autodownload.enabled",
	} {
		if settings[key] != Defaults[key] {
			t.Errorf("%s = %q, want %q", key, settings[key], Defaults[key])
		}
	}
}

func TestSettingRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if got, _ := s.Setting(ctx, "missing.key"); got != "" {
		t.Fatalf("absent key returned %q, want empty", got)
	}

	if err := s.SetSetting(ctx, "a.b", "one"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSetting(ctx, "a.b", "two"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Setting(ctx, "a.b"); got != "two" {
		t.Fatalf("got %q, want the upserted value", got)
	}

	s.SetSetting(ctx, "n", "42")
	if got, _ := s.SettingInt(ctx, "n"); got != 42 {
		t.Fatalf("SettingInt = %d", got)
	}
	if got, _ := s.SettingInt(ctx, "missing"); got != 0 {
		t.Fatalf("SettingInt on absent key = %d, want 0", got)
	}
}

func anime(id int, title string) Anime {
	return Anime{ID: id, Romaji: title, Synonyms: "[]", Genres: "[]"}
}

func TestImportListAndRead(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	status := "CURRENT"
	res, err := s.ImportList(ctx,
		[]Anime{anime(100, "Sousou no Frieren"), anime(200, "Cowboy Bebop")},
		[]Entry{
			{ID: 1, AnimeID: 100, Status: &status, Progress: 7, Score: 90, UpdatedAt: 1000},
			{ID: 2, AnimeID: 200, Progress: 3, UpdatedAt: 1001},
		}, ImportMerge)
	if err != nil {
		t.Fatal(err)
	}
	if res.Anime != 2 || res.Entries != 2 {
		t.Fatalf("got %+v", res)
	}

	all := Paging{Page: 1, PerPage: 50}

	items, err := s.Library(ctx, LibraryFilter{}, all)
	if err != nil {
		t.Fatal(err)
	}
	if len(items.Items) != 2 {
		t.Fatalf("got %d items, want 2", len(items.Items))
	}

	filtered, err := s.Library(ctx, LibraryFilter{Status: "CURRENT"}, all)
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered.Items) != 1 || filtered.Items[0].ID != 100 {
		t.Fatalf("status filter returned %+v", filtered.Items)
	}
	// The count must reflect the filter, not the whole table.
	if filtered.Total != 1 {
		t.Errorf("filtered total = %d, want 1", filtered.Total)
	}
}

// A remote import must not overwrite a local change that has not been pushed.
func TestImportSkipsDirtyRows(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if _, err := s.ImportList(ctx, []Anime{anime(100, "Frieren")},
		[]Entry{{ID: 1, AnimeID: 100, Progress: 7, UpdatedAt: 1000}}, ImportMerge); err != nil {
		t.Fatal(err)
	}

	if _, err := s.w.ExecContext(ctx,
		`UPDATE list_entry SET progress = 9, dirty = 1 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}

	if _, err := s.ImportList(ctx, []Anime{anime(100, "Frieren")},
		[]Entry{{ID: 1, AnimeID: 100, Progress: 7, UpdatedAt: 2000}}, ImportMerge); err != nil {
		t.Fatal(err)
	}

	var progress, dirty int
	err := s.r.QueryRowContext(ctx,
		`SELECT progress, dirty FROM list_entry WHERE id = 1`).Scan(&progress, &dirty)
	if err != nil {
		t.Fatal(err)
	}
	if progress != 9 {
		t.Fatalf("progress = %d, want the local value 9 preserved", progress)
	}
	if dirty != 1 {
		t.Fatalf("dirty = %d, want the row still flagged for push", dirty)
	}
}

// Replacing exists precisely to do what merging refuses to: make the local list
// match the tracker, unpushed edits included.
func TestReplaceOverwritesDirtyRows(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if _, err := s.ImportList(ctx, []Anime{anime(100, "Frieren")},
		[]Entry{{ID: 1, AnimeID: 100, Progress: 7, UpdatedAt: 1000}}, ImportMerge); err != nil {
		t.Fatal(err)
	}
	if _, err := s.w.ExecContext(ctx,
		`UPDATE list_entry SET progress = 9, dirty = 1 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}

	if _, err := s.ImportList(ctx, []Anime{anime(100, "Frieren")},
		[]Entry{{ID: 1, AnimeID: 100, Progress: 7, UpdatedAt: 2000}}, ImportReplace); err != nil {
		t.Fatal(err)
	}

	var progress, dirty int
	err := s.r.QueryRowContext(ctx,
		`SELECT progress, dirty FROM list_entry WHERE id = 1`).Scan(&progress, &dirty)
	if err != nil {
		t.Fatal(err)
	}
	if progress != 7 || dirty != 0 {
		t.Fatalf("progress = %d dirty = %d, want the remote value 7 and a clean row", progress, dirty)
	}
}

func TestReplaceDropsEntriesTheTrackerNoLongerHas(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	both := []Anime{anime(100, "Frieren"), anime(200, "Bebop")}
	_, err := s.ImportList(ctx, both, []Entry{
		{ID: 1, AnimeID: 100, Progress: 7, UpdatedAt: 1000},
		{ID: 2, AnimeID: 200, Progress: 3, UpdatedAt: 1000},
	}, ImportMerge)
	if err != nil {
		t.Fatal(err)
	}

	// Bebop was removed upstream.
	res, err := s.ImportList(ctx, both,
		[]Entry{{ID: 1, AnimeID: 100, Progress: 8, UpdatedAt: 2000}}, ImportReplace)
	if err != nil {
		t.Fatal(err)
	}
	if res.Removed != 1 {
		t.Fatalf("removed = %d, want 1", res.Removed)
	}

	var left int
	if err := s.r.QueryRowContext(ctx,
		`SELECT count(*) FROM list_entry`).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 1 {
		t.Fatalf("%d entries left, want only the one the tracker still lists", left)
	}
}

// Merging is the safe default and must never delete: a show added locally and
// never pushed is the whole reason someone picks it.
func TestMergeKeepsEntriesTheTrackerDoesNotHave(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	both := []Anime{anime(100, "Frieren"), anime(200, "Bebop")}
	_, err := s.ImportList(ctx, both, []Entry{
		{ID: 1, AnimeID: 100, Progress: 7, UpdatedAt: 1000},
		{ID: 2, AnimeID: 200, Progress: 3, UpdatedAt: 1000},
	}, ImportMerge)
	if err != nil {
		t.Fatal(err)
	}

	res, err := s.ImportList(ctx, both,
		[]Entry{{ID: 1, AnimeID: 100, Progress: 8, UpdatedAt: 2000}}, ImportMerge)
	if err != nil {
		t.Fatal(err)
	}
	if res.Removed != 0 {
		t.Fatalf("merge removed %d entries", res.Removed)
	}

	var left int
	if err := s.r.QueryRowContext(ctx, `SELECT count(*) FROM list_entry`).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 2 {
		t.Fatalf("%d entries left, want both kept", left)
	}
}

func TestImportIsAtomic(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// The second entry references an anime that was never inserted, so the
	// foreign key rejects it and the whole batch must roll back.
	_, err := s.ImportList(ctx, []Anime{anime(100, "Frieren")},
		[]Entry{
			{ID: 1, AnimeID: 100, Progress: 7},
			{ID: 2, AnimeID: 999, Progress: 1},
		}, ImportMerge)
	if err == nil {
		t.Fatal("expected a foreign key violation")
	}

	var n int
	if err := s.r.QueryRowContext(ctx, `SELECT count(*) FROM list_entry`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("%d rows survived a failed import", n)
	}
}

func TestFullTextSearchIndexesEveryTitle(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	english := "Frieren: Beyond Journey's End"
	native := "葬送のフリーレン"
	a := anime(100, "Sousou no Frieren")
	a.English = &english
	a.Native = &native
	a.Synonyms = `["Frieren at the Funeral"]`

	if _, err := s.ImportList(ctx, []Anime{a}, nil, ImportMerge); err != nil {
		t.Fatal(err)
	}

	// Latin terms go through FTS, CJK and short terms through the LIKE
	// fallback. Every one must resolve to the same row.
	for _, q := range []string{"sousou", "frier", "beyond", "funeral", "葬送", "フリーレン", "no"} {
		got, err := s.SearchLocal(ctx, q, 0)
		if err != nil {
			t.Fatalf("%q: %v", q, err)
		}
		if len(got) != 1 || got[0].ID != 100 {
			t.Fatalf("%q returned %d results, want the one row", q, len(got))
		}
	}
}

func TestSearchLocalRejectsNothingAndNonsense(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if _, err := s.ImportList(ctx, []Anime{anime(100, "Frieren")}, nil, ImportMerge); err != nil {
		t.Fatal(err)
	}

	if got, err := s.SearchLocal(ctx, "   ", 0); err != nil || len(got) != 0 {
		t.Fatalf("blank query: %d results, err %v", len(got), err)
	}

	// FTS5 treats these as operators; unescaped they are a syntax error.
	for _, q := range []string{`"unbalanced`, "AND OR NOT", "a*b(c)"} {
		if _, err := s.SearchLocal(ctx, q, 0); err != nil {
			t.Fatalf("%q should not error: %v", q, err)
		}
	}
}

func TestFullTextIndexFollowsUpdates(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if _, err := s.ImportList(ctx, []Anime{anime(100, "Original Title")}, nil, ImportMerge); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ImportList(ctx, []Anime{anime(100, "Renamed Title")}, nil, ImportMerge); err != nil {
		t.Fatal(err)
	}

	var stale int
	err := s.r.QueryRowContext(ctx,
		`SELECT count(*) FROM anime_fts WHERE anime_fts MATCH 'Original'`).Scan(&stale)
	if err != nil {
		t.Fatal(err)
	}
	if stale != 0 {
		t.Fatal("the old title is still searchable after a rename")
	}
}
