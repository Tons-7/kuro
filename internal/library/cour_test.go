package library

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"kuro/internal/db"
	"kuro/internal/indexer"
	"kuro/internal/metadata"
	"kuro/internal/score"
	"kuro/internal/store"
)

// Bleach: Thousand-Year Blood War is four AniList entries sharing a base title,
// each restarting at one, which TheTVDB files as one season counted through.
const (
	bleach       = 269
	tybw1        = 116674
	tybw2        = 159322
	tybw3        = 169755
	tybwCalamity = 185874
)

func bleachStore(t *testing.T) *store.Store {
	t.Helper()

	conn, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := conn.Migrate(); err != nil {
		t.Fatal(err)
	}
	st := store.New(conn)
	ctx := context.Background()

	entry := func(id int, romaji, english, start string, episodes int) store.Anime {
		tv := "TV"
		return store.Anime{
			ID: id, Romaji: romaji, English: &english, Format: &tv, StartDate: start,
			Episodes: &episodes, Synonyms: "[]", Genres: "[]",
		}
	}
	anime := []store.Anime{
		entry(bleach, "BLEACH", "Bleach", "2004-10-05", 366),
		entry(tybw1, "BLEACH: Sennen Kessen-hen", "BLEACH: Thousand-Year Blood War", "2022-10-11", 13),
		entry(tybw2, "BLEACH: Sennen Kessen-hen - Ketsubetsu-tan", "BLEACH: Thousand-Year Blood War - The Separation", "2023-07-08", 13),
		entry(tybw3, "BLEACH: Sennen Kessen-hen - Soukoku-tan", "BLEACH: Thousand-Year Blood War - The Conflict", "2024-10-05", 14),
		entry(tybwCalamity, "BLEACH: Sennen Kessen-hen - Kashin-tan", "BLEACH: Thousand-Year Blood War - The Calamity", "2026-07-25", 10),
	}
	if _, err := st.ImportList(ctx, anime, nil, store.ImportMerge); err != nil {
		t.Fatal(err)
	}

	var rels []store.Relation
	chain := []int{bleach, tybw1, tybw2, tybw3, tybwCalamity}
	for i := 1; i < len(chain); i++ {
		rels = append(rels, store.Relation{AnimeID: chain[i-1], RelatedID: chain[i], Kind: "SEQUEL"})
	}
	if err := st.SaveRelations(ctx, rels); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RebuildFranchises(ctx); err != nil {
		t.Fatal(err)
	}

	// Each entry counts from one; the cross-cour numbering is derived.
	episodes := func(id, count int) {
		var eps []metadata.Episode
		for n := 1; n <= count; n++ {
			eps = append(eps, metadata.Episode{Number: n})
		}
		if _, err := st.SaveEpisodes(ctx, id, eps); err != nil {
			t.Fatal(err)
		}
	}
	episodes(tybw1, 13)
	episodes(tybw2, 13)
	episodes(tybw3, 14)
	episodes(tybwCalamity, 10)
	return st
}

func TestCourTellsTheEntryFromItsSiblings(t *testing.T) {
	st := bleachStore(t)
	f := NewFinder(st, fixedIndexer{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := context.Background()

	titles, _ := st.SearchTitles(ctx, tybwCalamity)
	english, _ := st.EnglishTitle(ctx, tybwCalamity)
	req := f.numbering(ctx, Request{AnimeID: tybwCalamity, Episode: 5}, titles, english)

	if req.Alias.Tvdb != 45 || req.Alias.Absolute != 411 {
		t.Errorf("alias = %+v, want 45 / 411", req.Alias)
	}
	if !req.Cour.Later {
		t.Error("the fourth cour must count as a later one")
	}
	if !slices.Equal(req.Cour.Own, []string{"calamity", "kashin"}) {
		t.Errorf("own words = %v", req.Cour.Own)
	}
	for _, w := range []string{"soukoku", "conflict", "ketsubetsu", "separation"} {
		if !slices.Contains(req.Cour.Siblings, w) {
			t.Errorf("sibling words %v lack %q", req.Cour.Siblings, w)
		}
	}
	// Words every cour shares name the show, not a cour.
	for _, w := range []string{"bleach", "sennen", "kessen", "thousand"} {
		if slices.Contains(req.Cour.Siblings, w) || slices.Contains(req.Cour.Own, w) {
			t.Errorf("%q was taken for a cour marker", w)
		}
	}

	// The first cour is the one the bare name belongs to.
	titles, _ = st.SearchTitles(ctx, tybw1)
	english, _ = st.EnglishTitle(ctx, tybw1)
	first := f.numbering(ctx, Request{AnimeID: tybw1, Episode: 5}, titles, english)
	if first.Cour.Later || len(first.Alias.Numbers()) != 1 || first.Alias.Absolute != 371 {
		t.Errorf("first cour: later=%v alias=%+v", first.Cour.Later, first.Alias)
	}
}

// Without TheTVDB's numbering the alias is counted out of the franchise: the
// cours before this one give the season's count, everything before it the absolute.
func TestAliasesAreDerivedFromTheFranchise(t *testing.T) {
	st := bleachStore(t)
	f := NewFinder(st, fixedIndexer{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := context.Background()

	alias := func(id, episode int) store.EpisodeAlias {
		titles, _ := st.SearchTitles(ctx, id)
		english, _ := st.EnglishTitle(ctx, id)
		return f.numbering(ctx, Request{AnimeID: id, Episode: episode}, titles, english).Alias
	}

	// 13 + 13 + 14 cours before it, and 366 episodes of the original series.
	if got := alias(tybwCalamity, 2); got.Tvdb != 42 || got.Absolute != 408 {
		t.Errorf("The Calamity episode 2: %+v, want 42 / 408", got)
	}
	// No run before the first cour; the series before it still counts.
	if got := alias(tybw1, 5); got.Tvdb != 0 || got.Absolute != 371 {
		t.Errorf("Thousand-Year Blood War episode 5: %+v, want 0 / 371", got)
	}
	if got := alias(bleach, 5); got.Tvdb != 0 || got.Absolute != 0 {
		t.Errorf("the first entry has nothing to count: %+v", got)
	}

	// A catalogue that does carry the numbering is believed over the count.
	if _, err := st.SaveEpisodes(ctx, tybwCalamity, []metadata.Episode{
		{Number: 2, TvdbNumber: 99, Absolute: 999},
	}); err != nil {
		t.Fatal(err)
	}
	if got := alias(tybwCalamity, 2); got.Tvdb != 99 || got.Absolute != 999 {
		t.Errorf("catalogue alias = %+v, want 99 / 999", got)
	}
	// So is one saying the numbers agree: it is not a gap to fill.
	if _, err := st.SaveEpisodes(ctx, tybwCalamity, []metadata.Episode{
		{Number: 2, TvdbNumber: 2, Absolute: 2},
	}); err != nil {
		t.Fatal(err)
	}
	if got := alias(tybwCalamity, 2); got.Tvdb != 0 || got.Absolute != 0 {
		t.Errorf("catalogue says no alias, got %+v", got)
	}
}

// Cours either side of an entry that is not one leave no way to know where the
// count restarts, so only the absolute is offered: it counts everything.
func TestASplitRunOffersNoCountedThroughNumber(t *testing.T) {
	conn, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := conn.Migrate(); err != nil {
		t.Fatal(err)
	}
	st := store.New(conn)
	ctx := context.Background()

	tv, count := "TV", 12
	entry := func(id int, romaji, start string) store.Anime {
		return store.Anime{
			ID: id, Romaji: romaji, Format: &tv, StartDate: start,
			Episodes: &count, Synonyms: "[]", Genres: "[]",
		}
	}
	chain := []store.Anime{
		entry(1, "Some Show - First", "2020-01-01"),
		entry(2, "Another Show Entirely", "2021-01-01"),
		entry(3, "Some Show - Second", "2022-01-01"),
		entry(4, "Some Show - Third", "2023-01-01"),
	}
	if _, err := st.ImportList(ctx, chain, nil, store.ImportMerge); err != nil {
		t.Fatal(err)
	}
	var rels []store.Relation
	for i := 1; i < len(chain); i++ {
		rels = append(rels, store.Relation{AnimeID: chain[i-1].ID, RelatedID: chain[i].ID, Kind: "SEQUEL"})
	}
	if err := st.SaveRelations(ctx, rels); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RebuildFranchises(ctx); err != nil {
		t.Fatal(err)
	}

	f := NewFinder(st, fixedIndexer{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	titles, _ := st.SearchTitles(ctx, 4)
	english, _ := st.EnglishTitle(ctx, 4)
	got := f.numbering(ctx, Request{AnimeID: 4, Episode: 3}, titles, english).Alias
	if got.Tvdb != 0 || got.Absolute != 39 {
		t.Errorf("alias = %+v, want 0 / 39", got)
	}
}

// What each release is, for a request of The Calamity episode 5.
var calamityReleases = []struct {
	title     string
	accepted  bool
	confirmed bool
	numbers   []int
}{
	// A sibling cour's episode 5, and by far the most seeded "05".
	{"[Erai-raws] Bleach - Sennen Kessen Hen - Soukoku Tan - 05 [1080p][HEVC][Multiple Subtitle][2DC8C704].mkv", false, false, nil},
	{"[Group] Bleach - Sennen Kessen-hen - Ketsubetsu-tan - 45 [1080p].mkv", false, false, nil},
	// The first cour's episode 5: a bare name counts from the first cour.
	{"[SubsPlease] Bleach - Sennen Kessen-hen - 05 (1080p) [ABCD1234].mkv", false, false, nil},
	{"[Group] Bleach - Sennen Kessen-hen (01-13) [Batch][1080p]", false, false, nil},
	// Counted through the season under the bare name.
	{"[DKB] Bleach - Sennen Kessen-hen - 45 [1080p][HEVC x265 10bit][Multi-Subs][5069A3B6].mkv", true, true, []int{45, 411}},
	{"[Group] Bleach - Sennen Kessen-hen (41-50) [Batch][1080p]", true, true, []int{45, 411}},
	// Named for this cour, so its own count applies.
	{"[Erai-raws] Bleach - Sennen Kessen Hen - Kashin Tan - 05 [1080p][HEVC].mkv", true, true, []int{5, 45, 411}},
	{"[Group] Bleach - Thousand-Year Blood War - The Calamity - 05 [1080p].mkv", true, true, []int{5, 45, 411}},
	// The original series' episode 45 is not Thousand-Year Blood War's.
	{"[Group] Bleach - 45 [1080p].mkv", false, false, nil},
	// Absolute numbering is unique across the whole show.
	{"[Group] Bleach - 411 [1080p].mkv", true, true, []int{45, 411}},
	// An unnumbered pack is settled by its file list, by the numbers a bare
	// pack can carry.
	{"[Group] Bleach - Sennen Kessen-hen [1080p][BD]", true, false, []int{45, 411}},
	// The original series' pack holds a file numbered 45: its own episode 45.
	{"[Group] Bleach [1080p][BD]", false, false, nil},
	// Naming the cour is enough, however the show is shortened.
	{"[Group] Bleach TYBW - The Calamity [BD]", true, false, []int{5, 45, 411}},
	// The counted-through number under a stated season, for this show only.
	{"[Group] Bleach S17E45 [1080p].mkv", true, true, []int{45, 411}},
	{"[Group] Some Other Show S02E45 [1080p].mkv", false, false, nil},
}

func TestFindKeepsOnlyThisCoursEpisode(t *testing.T) {
	st := bleachStore(t)

	var results []indexer.Torrent
	for i, r := range calamityReleases {
		results = append(results, release(strings.Repeat("a", 39)+string(rune('0'+i)), r.title, 100))
	}
	f := NewFinder(st, fixedIndexer{results: results}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	found, err := f.Find(context.Background(), Request{
		AnimeID: tybwCalamity, Episode: 5, Prefs: score.DefaultPreferences(),
	})
	if err != nil {
		t.Fatal(err)
	}

	got := map[string]score.Result{}
	for _, r := range found.Results {
		got[r.Torrent.Title] = r
	}
	for _, want := range calamityReleases {
		r, ok := got[want.title]
		if ok != want.accepted {
			t.Errorf("%s: accepted=%v, want %v", want.title, ok, want.accepted)
			continue
		}
		if !ok {
			continue
		}
		if r.Confirmed != want.confirmed {
			t.Errorf("%s: confirmed=%v, want %v", want.title, r.Confirmed, want.confirmed)
		}
		if !slices.Equal(r.Numbers, want.numbers) {
			t.Errorf("%s: numbers=%v, want %v", want.title, r.Numbers, want.numbers)
		}
	}
	if found.Best == nil || !strings.Contains(found.Best.Torrent.Title, "Kashin Tan") &&
		!strings.Contains(found.Best.Torrent.Title, "Calamity") && !strings.Contains(found.Best.Torrent.Title, "[DKB]") {
		t.Errorf("best = %+v", found.Best)
	}

	// Searched under the season's count as well as its own.
	var seasonCount, own bool
	for _, q := range found.Queries {
		seasonCount = seasonCount || strings.HasSuffix(q, " 45")
		own = own || strings.HasSuffix(q, " 05")
	}
	if !seasonCount || !own {
		t.Errorf("queries %v should carry both 45 and 05", found.Queries)
	}
}

// The first cour owns the bare name: its own count applies without a marker,
// and a later cour's release or the season's count is something else.
func TestFindForTheFirstCourTakesTheBareName(t *testing.T) {
	st := bleachStore(t)
	results := []indexer.Torrent{
		release(strings.Repeat("b", 40), "[SubsPlease] Bleach - Sennen Kessen-hen - 05 (1080p) [ABCD1234].mkv", 100),
		release(strings.Repeat("c", 40), "[Erai-raws] Bleach - Sennen Kessen Hen - Soukoku Tan - 05 [1080p].mkv", 100),
		release(strings.Repeat("d", 40), "[DKB] Bleach - Sennen Kessen-hen - 45 [1080p].mkv", 100),
	}
	f := NewFinder(st, fixedIndexer{results: results}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	found, err := f.Find(context.Background(), Request{
		AnimeID: tybw1, Episode: 5, Prefs: score.DefaultPreferences(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(found.Results) != 1 || !strings.HasPrefix(found.Results[0].Torrent.Title, "[SubsPlease]") {
		t.Errorf("results = %+v, want only the SubsPlease release", found.Results)
	}
	if len(found.Results) == 1 && !slices.Equal(found.Results[0].Numbers, []int{5, 371}) {
		t.Errorf("numbers = %v, want [5 371]", found.Results[0].Numbers)
	}
}
