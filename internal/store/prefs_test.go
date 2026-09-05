package store

import (
	"context"
	"testing"
)

func TestPrefsFallBackToDefaults(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	p, err := s.Prefs(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}

	if got := p.String("playback.anime4k_mode"); got != "A" {
		t.Errorf("anime4k mode = %q, want the default", got)
	}
	// Everything automatic is opt-in.
	for _, key := range []string{
		"playback.autoplay", "playback.autonext",
		"playback.autoskip_op", "playback.autoskip_ed", "playback.anime4k",
	} {
		if p.Bool(key) {
			t.Errorf("%s should default off", key)
		}
	}
	if got, want := p.Int64("cache.budget_bytes"), int64(5<<30); got != want {
		t.Errorf("cache budget = %d, want %d", got, want)
	}
	if got := p.Float("sync.progress_at"); got != 0.9 {
		t.Errorf("progress threshold = %v", got)
	}

	// A key the database has never seen must still resolve.
	if got := p.String("audio.prefer"); got != "sub" {
		t.Errorf("audio preference = %q", got)
	}
}

// A per-show override beats the global value, and removing it falls back.
func TestAnimePrefOverridesGlobal(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedAnime(t, s, 1)

	if err := s.SetSetting(ctx, "playback.anime4k_mode", "B"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAnimePref(ctx, 1, "playback.anime4k_mode", "C+A"); err != nil {
		t.Fatal(err)
	}

	global, _ := s.Prefs(ctx, 0)
	if got := global.String("playback.anime4k_mode"); got != "B" {
		t.Errorf("global = %q, want B", got)
	}

	perShow, _ := s.Prefs(ctx, 1)
	if got := perShow.String("playback.anime4k_mode"); got != "C+A" {
		t.Errorf("per-show = %q, want C+A", got)
	}

	// An override on one show must not leak to another.
	other, _ := s.Prefs(ctx, 999)
	if got := other.String("playback.anime4k_mode"); got != "B" {
		t.Errorf("other show = %q, want the global value", got)
	}

	if err := s.SetAnimePref(ctx, 1, "playback.anime4k_mode", ""); err != nil {
		t.Fatal(err)
	}
	cleared, _ := s.Prefs(ctx, 1)
	if got := cleared.String("playback.anime4k_mode"); got != "B" {
		t.Errorf("after clearing = %q, want the global value back", got)
	}
}

func TestPrefsAllMergesDefaults(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	p, _ := s.Prefs(ctx, 0)
	all := p.All()

	for key := range Defaults {
		if _, ok := all[key]; !ok {
			t.Errorf("%q missing from the resolved set", key)
		}
	}
}

func TestBookmarks(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedAnime(t, s, 1)
	seedAnime(t, s, 2)

	if err := s.SetBookmark(ctx, 1, Bookmark{Favourite: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetBookmark(ctx, 2, Bookmark{Favourite: true, Hidden: true}); err != nil {
		t.Fatal(err)
	}

	got, err := s.Bookmarks(ctx, Paging{Page: 1, PerPage: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 1 || got.Items[0].ID != 1 {
		t.Fatalf("got %d bookmarks; hidden entries should be excluded", len(got.Items))
	}
	if got.Total != 1 {
		t.Errorf("total = %d, want 1", got.Total)
	}

	// Unfavouriting removes it from the list without deleting the row.
	if err := s.SetBookmark(ctx, 1, Bookmark{Favourite: false}); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.Bookmarks(ctx, Paging{Page: 1, PerPage: 50}); len(got.Items) != 0 {
		t.Fatalf("got %d bookmarks after unfavouriting", len(got.Items))
	}
}

func TestRecentlyWatchedOrdersByLastPlayed(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedAnime(t, s, 1)
	seedAnime(t, s, 2)

	s.w.ExecContext(ctx, `INSERT INTO playback (anime_id, ep_key, position_s, last_played_at)
	                      VALUES (1,'1',100,1000), (2,'1',200,2000)`)

	got, err := s.RecentlyWatched(ctx, Paging{Page: 1, PerPage: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 2 {
		t.Fatalf("got %d entries", len(got.Items))
	}
	if got.Items[0].ID != 2 {
		t.Fatalf("first entry is %d, want the most recently played", got.Items[0].ID)
	}
	if got.Items[0].Resume == nil || got.Items[0].Resume.Position != 200 {
		t.Fatalf("resume position missing: %+v", got.Items[0].Resume)
	}
}

// A page must report whether more rows exist without the caller fetching them.
func TestPaginationReportsMore(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	for id := 1; id <= 5; id++ {
		seedAnime(t, s, id)
		if err := s.SetBookmark(ctx, id, Bookmark{Favourite: true}); err != nil {
			t.Fatal(err)
		}
	}

	first, err := s.Bookmarks(ctx, Paging{Page: 1, PerPage: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 2 {
		t.Fatalf("page 1 returned %d items, want the page size", len(first.Items))
	}
	if !first.HasMore {
		t.Error("page 1 of 5 should report more")
	}
	if first.Total != 5 {
		t.Errorf("total = %d, want 5", first.Total)
	}

	last, err := s.Bookmarks(ctx, Paging{Page: 3, PerPage: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(last.Items) != 1 {
		t.Fatalf("final page returned %d items", len(last.Items))
	}
	if last.HasMore {
		t.Error("the final page should not report more")
	}

	// Past the end is empty, not an error.
	beyond, err := s.Bookmarks(ctx, Paging{Page: 99, PerPage: 2})
	if err != nil || len(beyond.Items) != 0 {
		t.Fatalf("beyond the end: %d items, err %v", len(beyond.Items), err)
	}
}

// Local state references anime(id), but that table only holds the imported
// list. Following anything from the wider corpus has to work.
func TestLocalStateWorksForCorpusOnlyAnime(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if _, err := s.SaveCorpus(ctx, []CorpusEntry{
		corpusEntry(154587, "Sousou no Frieren", "Frieren: Beyond Journey's End"),
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.SetFollow(ctx, Follow{AnimeID: 154587}, true); err != nil {
		t.Fatalf("follow a corpus-only anime: %v", err)
	}
	if err := s.SetBookmark(ctx, 154587, Bookmark{Favourite: true}); err != nil {
		t.Fatalf("bookmark a corpus-only anime: %v", err)
	}
	if err := s.SetAnimePref(ctx, 154587, "playback.anime4k_mode", "C+A"); err != nil {
		t.Fatalf("set a pref on a corpus-only anime: %v", err)
	}

	p, _ := s.Prefs(ctx, 154587)
	if got := p.String("playback.anime4k_mode"); got != "C+A" {
		t.Fatalf("override = %q, want C+A", got)
	}

	// The materialised row should carry the real title, not a placeholder.
	var title string
	if err := s.r.QueryRowContext(ctx,
		`SELECT title_romaji FROM anime WHERE id = 154587`).Scan(&title); err != nil {
		t.Fatal(err)
	}
	if title != "Sousou no Frieren" {
		t.Errorf("title = %q, want the primary corpus title", title)
	}

	if got, _ := s.Bookmarks(ctx, Paging{Page: 1, PerPage: 50}); len(got.Items) != 1 {
		t.Fatalf("got %d bookmarks", len(got.Items))
	}
}

// A row already imported from AniList must not be overwritten by the sparse
// corpus version.
func TestEnsureAnimeDoesNotClobberImportedRow(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s.SaveCorpus(ctx, []CorpusEntry{corpusEntry(1, "Corpus Title")})
	s.w.ExecContext(ctx,
		`INSERT INTO anime (id, title_romaji, synced_at) VALUES (1, 'Imported Title', 99)`)

	if err := s.EnsureAnime(ctx, 1); err != nil {
		t.Fatal(err)
	}

	var title string
	var synced int
	s.r.QueryRowContext(ctx, `SELECT title_romaji, synced_at FROM anime WHERE id = 1`).Scan(&title, &synced)
	if title != "Imported Title" || synced != 99 {
		t.Fatalf("imported row was overwritten: %q synced=%d", title, synced)
	}
}

func seedAnime(t *testing.T, s *Store, id int) {
	t.Helper()
	_, err := s.w.Exec(
		`INSERT INTO anime (id, title_romaji, synced_at) VALUES (?, ?, 0)
		 ON CONFLICT DO NOTHING`, id, "Show")
	if err != nil {
		t.Fatal(err)
	}
}
