package store

import (
	"context"
	"testing"
)

func TestPushedRecordsWhatTheTrackerWasTold(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, seen, err := s.Pushed(ctx, "mal", 1); err != nil || seen {
		t.Fatalf("seen=%v err=%v before any push", seen, err)
	}
	s.ImportList(ctx, []Anime{{ID: 1, Romaji: "Show"}}, nil, ImportMerge)
	if err := s.MarkPushed(ctx, "mal", 1, 4, "CURRENT", 70); err != nil {
		t.Fatal(err)
	}
	e, seen, err := s.Pushed(ctx, "mal", 1)
	if err != nil || !seen || e.Progress != 4 || e.Status != "CURRENT" || e.Score != 70 {
		t.Fatalf("pushed = %+v seen=%v err=%v", e, seen, err)
	}
	if _, seen, _ := s.Pushed(ctx, "anilist", 1); seen {
		t.Fatal("trackers must not share records")
	}
}

func TestScoreChangeAloneIsPendingForMAL(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	mal := 52991
	s.ImportList(ctx, []Anime{{ID: 1, Romaji: "Show", MalID: &mal}}, nil, ImportMerge)
	s.MarkWatched(ctx, 1, 3)
	s.MarkPushed(ctx, "mal", 1, 3, "CURRENT", 0)
	if p, _ := s.PendingMALPushes(ctx, "mal", 10); len(p) != 0 {
		t.Fatalf("pending = %+v before any change", p)
	}
	s.SetScore(ctx, 1, 90)
	p, _ := s.PendingMALPushes(ctx, "mal", 10)
	if len(p) != 1 || p[0].Score != 90 {
		t.Fatalf("pending = %+v, want the rescored entry", p)
	}
}

// Negative ids are MAL-only titles; the anime row comes from the corpus so an
// entry can hang on it, mal_id included for the MAL push.
func TestMALOnlyAnimeGetRowsFromTheCorpus(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.SaveCorpus(ctx, []CorpusEntry{{AniListID: -777, MalID: 777, Kind: "TV", Episodes: 13,
		Titles: []CorpusTitle{{Text: "MAL only", Kind: "primary"}}}}); err != nil {
		t.Fatal(err)
	}

	changed, err := s.ApplyRemote(ctx, RemoteEntry{AnimeID: -777, Status: "PLANNING"})
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if e := entryOf(t, s, -777); e.Status != "PLANNING" || e.ID >= 0 {
		t.Fatalf("entry = %+v", e)
	}
	var malID int
	var title string
	if err := s.r.QueryRow(`SELECT mal_id, title_romaji FROM anime WHERE id = -777`).Scan(&malID, &title); err != nil {
		t.Fatal(err)
	}
	if malID != 777 || title != "MAL only" {
		t.Fatalf("anime row = %d %q", malID, title)
	}

	if _, err := s.MarkWatched(ctx, -777, 2); err != nil {
		t.Fatal(err)
	}
	if e := entryOf(t, s, -777); e.Progress != 2 || e.Status != "CURRENT" {
		t.Fatalf("entry = %+v", e)
	}
	if err := s.SetScore(ctx, -777, 60); err != nil {
		t.Fatal(err)
	}
	if err := s.SetListStatus(ctx, -777, "PAUSED", -1); err != nil {
		t.Fatal(err)
	}
	if e := entryOf(t, s, -777); e.Score != 60 || e.Status != "PAUSED" {
		t.Fatalf("entry = %+v", e)
	}
	pending, _ := s.PendingMALPushes(ctx, "mal", 10)
	if len(pending) != 1 || pending[0].RemoteID != 777 {
		t.Fatalf("pending = %+v", pending)
	}
	if dirty, _ := s.DirtyEntries(ctx, 0); len(dirty) != 0 {
		t.Fatalf("offered to AniList: %+v", dirty)
	}

	rows, err := s.Episodes(ctx, -777)
	if err != nil || len(rows) != 13 || !rows[1].Watched || rows[2].Watched {
		t.Fatalf("episodes = %d rows err=%v", len(rows), err)
	}
}

// A show the corpus has never seen still gets a placeholder row, whatever the
// sign of its id, so nothing keyed on anime(id) can fail.
func TestEnsureAnimePlaceholderForUnknownIDs(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for _, id := range []int{-5, 5} {
		if err := s.EnsureAnime(ctx, id); err != nil {
			t.Fatal(err)
		}
		var title string
		if err := s.r.QueryRow(`SELECT title_romaji FROM anime WHERE id = ?`, id).Scan(&title); err != nil || title != "Unknown" {
			t.Fatalf("id %d: %q %v", id, title, err)
		}
	}
	if err := s.EnsureAnime(ctx, 0); err != nil {
		t.Fatal(err)
	}
	var n int
	s.r.QueryRow(`SELECT count(*) FROM anime WHERE id = 0`).Scan(&n)
	if n != 0 {
		t.Fatal("id 0 must never get a row")
	}
}

func TestUnwatchWithoutAnEntryIsHarmless(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for _, id := range []int{1, 0} {
		if err := s.Unwatch(ctx, id, 3); err != nil {
			t.Fatal(err)
		}
	}
	if e := entryOf(t, s, 1); e.AnimeID != 0 {
		t.Fatalf("unwatch created an entry: %+v", e)
	}
}

func TestSetScoreRejectsBadInput(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for _, c := range []struct{ id, score int }{{0, 50}, {1, -1}, {1, 101}} {
		if err := s.SetScore(ctx, c.id, c.score); err != nil {
			t.Fatal(err)
		}
	}
	if e := entryOf(t, s, 1); e.AnimeID != 0 {
		t.Fatalf("bad input created an entry: %+v", e)
	}
}
