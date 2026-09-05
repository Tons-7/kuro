package store

import (
	"context"
	"testing"
	"time"

	"kuro/internal/metadata"
)

func seedFillerShow(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	mal := 900
	ep := 6
	s.ImportList(ctx, []Anime{{ID: 1, Romaji: "Show", MalID: &mal, Episodes: &ep}}, nil, ImportMerge)
	var eps []metadata.Episode
	for n := 1; n <= 6; n++ {
		eps = append(eps, metadata.Episode{Number: n})
	}
	s.SaveEpisodes(ctx, 1, eps)
	// 2 filler, 3 recap, 4 mixed (kept), 5 filler by dataset but corrected by hand.
	s.SaveFillers(ctx, []metadata.Filler{{MalID: 900, Episode: 2, Kind: "filler"}, {MalID: 900, Episode: 4, Kind: "mixed"}, {MalID: 900, Episode: 5, Kind: "filler"}})
	s.SaveFlags(ctx, 900, []metadata.EpisodeFlags{{Episode: 3, Recap: true}})
	s.w.ExecContext(ctx, `UPDATE filler SET user_kind = 'manga-canon' WHERE mal_id = 900 AND number = 5`)
}

func TestNextEpisodeCountsUpWithoutTheFillerRule(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedFillerShow(t, s)

	for after, want := range map[int]int{0: 1, 1: 2, 5: 6, 6: 0, 9: 0} {
		if got, _ := s.NextEpisode(ctx, 1, after); got != want {
			t.Errorf("after %d: next = %d, want %d", after, got, want)
		}
	}
}

func TestNextEpisodeSkipsFillerAndRecapsWhenAsked(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedFillerShow(t, s)
	s.SetSetting(ctx, "playback.skip_filler", "true")

	// 2 is filler, 3 a recap; 4 is mixed (kept); 5 was corrected to canon by hand.
	for after, want := range map[int]int{1: 4, 2: 4, 3: 4, 4: 5, 5: 6, 6: 0} {
		if got, _ := s.NextEpisode(ctx, 1, after); got != want {
			t.Errorf("after %d: next = %d, want %d", after, got, want)
		}
	}

	// Per-show override wins over the global switch.
	s.SetAnimePref(ctx, 1, "playback.skip_filler", "false")
	if got, _ := s.NextEpisode(ctx, 1, 1); got != 2 {
		t.Fatalf("override off: next = %d, want 2", got)
	}
}

func TestNextEpisodeWithoutEpisodeDataFallsBackToTheCount(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	ep := 3
	s.ImportList(ctx, []Anime{{ID: 2, Romaji: "Bare", Episodes: &ep}}, nil, ImportMerge)
	s.SetSetting(ctx, "playback.skip_filler", "true")

	for after, want := range map[int]int{0: 1, 2: 3, 3: 0} {
		if got, _ := s.NextEpisode(ctx, 2, after); got != want {
			t.Errorf("after %d: next = %d, want %d", after, got, want)
		}
	}
	// Unknown count and no rows: always the next number.
	if got, _ := s.NextEpisode(ctx, 3, 40); got != 41 {
		t.Fatalf("unknown show: next = %d", got)
	}
}

// An airing show's rows lag its count; the rule must not call it finished.
func TestNextEpisodeOffersUncataloguedEpisodesWhileSkipping(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	mal := 902
	ep := 12
	s.ImportList(ctx, []Anime{{ID: 1, Romaji: "Airing", MalID: &mal, Episodes: &ep}}, nil, ImportMerge)
	s.SaveEpisodes(ctx, 1, []metadata.Episode{{Number: 1}, {Number: 2}, {Number: 3}, {Number: 4}})
	s.SaveFillers(ctx, []metadata.Filler{{MalID: 902, Episode: 4, Kind: "filler"}})
	s.SetSetting(ctx, "playback.skip_filler", "true")

	for after, want := range map[int]int{3: 5, 4: 5, 7: 8, 12: 0} {
		if got, _ := s.NextEpisode(ctx, 1, after); got != want {
			t.Errorf("after %d: next = %d, want %d", after, got, want)
		}
	}
}

func TestNextEpisodeEndsWhenOnlyFillerRemains(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	mal := 901
	ep := 3
	s.ImportList(ctx, []Anime{{ID: 1, Romaji: "Show", MalID: &mal, Episodes: &ep}}, nil, ImportMerge)
	s.SaveEpisodes(ctx, 1, []metadata.Episode{{Number: 1}, {Number: 2}, {Number: 3}})
	s.SaveFillers(ctx, []metadata.Filler{{MalID: 901, Episode: 2, Kind: "filler"}, {MalID: 901, Episode: 3, Kind: "filler"}})
	s.SetSetting(ctx, "playback.skip_filler", "true")
	if got, _ := s.NextEpisode(ctx, 1, 1); got != 0 {
		t.Fatalf("next = %d, want the show to end", got)
	}
}

func TestLibrarySortAndFilter(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	air1, air2 := 100, 50
	s.ImportList(ctx, []Anime{
		{ID: 1, Romaji: "Zeta", English: ptrStr("Alpha Show"), NextAiringAt: &air1},
		{ID: 2, Romaji: "Beta", Synonyms: `["Cake"]`, NextAiringAt: &air2},
		{ID: 3, Romaji: "Gamma"},
	}, []Entry{
		{ID: 11, AnimeID: 1, Status: ptrStr("CURRENT"), Progress: 2, Score: 50, UpdatedAt: 1},
		{ID: 12, AnimeID: 2, Status: ptrStr("CURRENT"), Progress: 9, Score: 90, UpdatedAt: 2},
		{ID: 13, AnimeID: 3, Status: ptrStr("COMPLETED"), Progress: 5, Score: 70, UpdatedAt: 3},
	}, ImportMerge)
	all := Paging{Page: 1, PerPage: 10}
	ids := func(f LibraryFilter) []int {
		page, err := s.Library(ctx, f, all)
		if err != nil {
			t.Fatal(err)
		}
		var out []int
		for _, it := range page.Items {
			out = append(out, it.ID)
		}
		return out
	}
	eq := func(name string, got, want []int) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("%s: %v, want %v", name, got, want)
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("%s: %v, want %v", name, got, want)
			}
		}
	}

	eq("title", ids(LibraryFilter{Sort: "title"}), []int{1, 2, 3})
	eq("score", ids(LibraryFilter{Sort: "score"}), []int{2, 3, 1})
	eq("progress", ids(LibraryFilter{Sort: "progress"}), []int{2, 3, 1})
	eq("airing puts unknown last", ids(LibraryFilter{Sort: "airing"}), []int{2, 1, 3})
	eq("unknown sort falls back", ids(LibraryFilter{Sort: "bogus"}), ids(LibraryFilter{}))
	eq("query on english", ids(LibraryFilter{Query: "alpha"}), []int{1})
	eq("query on synonym", ids(LibraryFilter{Query: "cake"}), []int{2})
	eq("query with status", ids(LibraryFilter{Query: "a", Status: "COMPLETED"}), []int{3})

	page, _ := s.Library(ctx, LibraryFilter{Query: "a", Status: "CURRENT"}, all)
	if page.Total != 2 {
		t.Fatalf("total = %d, want the filtered count", page.Total)
	}
}

func TestWatchStatsAccumulateFromPlaybackReports(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	ep := 2
	s.ImportList(ctx, []Anime{{ID: 1, Romaji: "Show", Episodes: &ep}}, nil, ImportMerge)

	empty, err := s.WatchStats(ctx, time.Now())
	if err != nil || empty.TotalSeconds != 0 || len(empty.Days) != 30 {
		t.Fatalf("empty stats = %+v err=%v", empty, err)
	}

	// Ten reports of two minutes: only played time counts, never the position.
	// The tenth crosses the watched threshold, which is what Episodes counts.
	var watched bool
	for range 10 {
		watched, _ = s.SavePlayback(ctx, PlaybackState{AnimeID: 1, EpKey: "1", Position: 1400, Duration: 1440, Played: 120})
	}
	if !watched {
		t.Fatal("precondition: episode 1 should count as watched")
	}
	// A seek report with nothing played adds nothing; an oversized one is capped.
	s.SavePlayback(ctx, PlaybackState{AnimeID: 1, EpKey: "2", Position: 500, Duration: 1440, Played: 0})
	s.SavePlayback(ctx, PlaybackState{AnimeID: 1, EpKey: "2", Position: 900, Duration: 1440, Played: 9999})
	s.SetListStatus(ctx, 1, "COMPLETED", -1)

	// Taken after the writes so a midnight in between cannot move "today".
	now := time.Now()
	got, err := s.WatchStats(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if got.TotalSeconds != 1320 || got.WeekSeconds != 1320 || got.MonthSeconds != 1320 {
		t.Fatalf("seconds = %+v, want 10×120 + 120 capped", got)
	}
	if got.Episodes != 1 || got.Completed != 1 {
		t.Fatalf("episodes=%d completed=%d", got.Episodes, got.Completed)
	}
	if last := got.Days[29]; last.Day != now.Format("2006-01-02") || last.Seconds != 1320 {
		t.Fatalf("today = %+v", last)
	}

	// Outside the windows it still counts as all-time.
	if _, err := s.w.ExecContext(ctx, `INSERT INTO watch_time (day, seconds) VALUES (?, 600)`,
		now.AddDate(0, 0, -45).Format("2006-01-02")); err != nil {
		t.Fatal(err)
	}
	got, _ = s.WatchStats(ctx, now)
	if got.TotalSeconds != 1920 || got.MonthSeconds != 1320 {
		t.Fatalf("windows = %+v", got)
	}
}

func ptrStr(v string) *string { return &v }
