package library

import (
	"context"
	"fmt"
	"testing"

	"kuro/internal/parse"
	"kuro/internal/store"
)

// chainStore seeds a franchise of TV series in broadcast order, linked as sequels.
func chainStore(t *testing.T, titles ...[2]string) *store.Store {
	t.Helper()
	st := prefetchStore(t)
	ctx := context.Background()
	tv := "TV"
	var anime []store.Anime
	var rels []store.Relation
	for i, pair := range titles {
		eps := 13
		english := pair[1]
		anime = append(anime, store.Anime{ID: i + 1, Romaji: pair[0], English: &english, Format: &tv,
			StartDate: fmt.Sprintf("20%02d-01-01", 15+i), Episodes: &eps, Synonyms: "[]", Genres: "[]"})
		if i > 0 {
			rels = append(rels, store.Relation{AnimeID: i, RelatedID: i + 1, Kind: "SEQUEL"})
		}
	}
	if _, err := st.ImportList(ctx, anime, nil, store.ImportMerge); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveRelations(ctx, nil, rels); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RebuildFranchises(ctx); err != nil {
		t.Fatal(err)
	}
	return st
}

func request(t *testing.T, st *store.Store, animeID, episode int) Request {
	t.Helper()
	ctx := context.Background()
	f := NewFinder(st, fixedIndexer{}, discard())
	titles, _ := st.SearchTitles(ctx, animeID)
	english, _ := st.EnglishTitle(ctx, animeID)
	return f.numbering(ctx, Request{AnimeID: animeID, Episode: episode}, titles, english)
}

// With the franchise known, "Overlord IV - 05" is the fourth season's fifth episode, however the groups
// spell it; the earlier seasons' bare releases are not.
func TestNumeralNamesTheSeasonAcrossAFranchise(t *testing.T) {
	st := chainStore(t, [2]string{"Overlord", "Overlord"}, [2]string{"Overlord II", "Overlord II"},
		[2]string{"Overlord III", "Overlord III"}, [2]string{"Overlord IV", "Overlord IV"})
	req := request(t, st, 4, 5)
	if req.Season != 4 {
		t.Fatalf("season = %d, want 4", req.Season)
	}
	for _, name := range []string{
		"[SubsPlease] Overlord IV - 05 (1080p) [A1B2C3D4].mkv",
		"[Erai-raws] Overlord IV - 05 [1080p][Multiple Subtitle]",
		"[Judas] Overlord IV - S04E05 [1080p]",
	} {
		if !verifies(parse.Parse(name), req) {
			t.Errorf("refused %s", name)
		}
	}
	for _, name := range []string{"[SubsPlease] Overlord II - 05 (1080p)", "[SubsPlease] Overlord - 05 (1080p)"} {
		if verifies(parse.Parse(name), req) {
			t.Errorf("accepted %s as season 4's", name)
		}
	}

	// And the other way: the first season's search does not take season four's episode.
	first := request(t, st, 1, 5)
	if verifies(parse.Parse("[SubsPlease] Overlord IV - 05 (1080p)"), first) {
		t.Error("season one accepted Overlord IV's episode 5")
	}
	if !verifies(parse.Parse("[SubsPlease] Overlord - 05 (1080p)"), first) {
		t.Error("season one refused its own bare release")
	}
}

// A digit numeral works the same once the franchise says this is a later entry.
func TestDigitNumeralNamesTheSeason(t *testing.T) {
	st := chainStore(t, [2]string{"Kono Subarashii Sekai ni Shukufuku wo!", "KonoSuba"},
		[2]string{"Kono Subarashii Sekai ni Shukufuku wo! 2", "KonoSuba 2"},
		[2]string{"Kono Subarashii Sekai ni Shukufuku wo! 3", "KonoSuba 3"})
	req := request(t, st, 3, 5)
	if req.Season != 3 {
		t.Fatalf("season = %d, want 3", req.Season)
	}
	if !verifies(parse.Parse("[SubsPlease] Kono Subarashii Sekai ni Shukufuku wo! 3 - 05 (1080p)"), req) {
		t.Error("refused the SubsPlease name")
	}
	if verifies(parse.Parse("[SubsPlease] Kono Subarashii Sekai ni Shukufuku wo! 2 - 05 (1080p)"), req) {
		t.Error("accepted season two's")
	}
}

// A number that is part of the show's name is not a season: Kaiju No. 8's "- 05" is episode 5 of season one.
func TestNameNumeralIsNotASeason(t *testing.T) {
	st := chainStore(t, [2]string{"Kaijuu 8-gou", "Kaiju No. 8"})
	req := request(t, st, 1, 5)
	if req.Season != 1 {
		t.Fatalf("season = %d", req.Season)
	}
	if !verifies(parse.Parse("[SubsPlease] Kaiju No. 8 - 05 (1080p)"), req) {
		t.Error("the show's own number was read as a season")
	}
}

// A sequel whose titles carry only a subtitle is still the franchise's third series, so its "S3" releases pass.
func TestSubtitledSequelAcceptsItsCountedSeason(t *testing.T) {
	st := chainStore(t, [2]string{"Dr. Stone", "Dr. Stone"}, [2]string{"Dr. Stone: Stone Wars", "Dr. Stone: Stone Wars"},
		[2]string{"Dr. Stone: New World", "Dr. Stone: New World"})
	req := request(t, st, 3, 5)
	if req.Season != 1 || req.Counted != 3 {
		t.Fatalf("season = %d counted = %d", req.Season, req.Counted)
	}
	for _, name := range []string{"[SubsPlease] Dr. Stone S3 - 05 (1080p)", "[Erai-raws] Dr. Stone New World - 05 [1080p]"} {
		if !verifies(parse.Parse(name), req) {
			t.Errorf("refused %s", name)
		}
	}
	if verifies(parse.Parse("[SubsPlease] Dr. Stone S2 - 05 (1080p)"), req) {
		t.Error("accepted another season's")
	}
}
