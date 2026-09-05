package library

import (
	"testing"

	"kuro/internal/parse"
)

// An AniList entry is one season numbered from one, so a caller that omits the
// season must not be handed a later season's episode 1.
func TestSeasonComesFromTheShowsOwnTitle(t *testing.T) {
	for _, tc := range []struct {
		title string
		want  int
	}{
		{"Sousou no Frieren", 1},
		{"Sousou no Frieren 2nd Season", 2},
		{"Kimetsu no Yaiba Season 3", 3},
		{"Bleach: Sennen Kessen-hen", 1},
	} {
		if got := parse.SeasonOf(tc.title); got != tc.want && !(tc.want == 1 && got == 0) {
			t.Errorf("%q: season %d, want %d", tc.title, got, tc.want)
		}
	}
}

// The rejection this enables: episode 1 of a first season against a release
// explicitly marked as a later season.
func TestLaterSeasonIsRejectedForAFirstSeasonRequest(t *testing.T) {
	rel := parse.Parse("[Judas] Frieren - S02E01.mkv")
	if rel.Season != 2 {
		t.Fatalf("parsed season %d, want 2", rel.Season)
	}
	if verifies(rel, Request{Episode: 1, Season: 1}) {
		t.Error("S02E01 should not satisfy season 1 episode 1")
	}
	if !verifies(rel, Request{Episode: 1, Season: 2}) {
		t.Error("S02E01 should satisfy season 2 episode 1")
	}
}
