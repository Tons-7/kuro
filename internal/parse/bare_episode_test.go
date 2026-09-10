package parse

import "testing"

// "Show 08 1080p": no dash, no E prefix. The number went unparsed and stayed
// in the title, which then named a different show than the one asked for.
func TestBareNumberBeforeTheQualityRun(t *testing.T) {
	cases := []struct {
		name    string
		title   string
		episode int
	}{
		{"[ShouryuuReppa] BLEACH: Sennen Kessen-hen 08 1080p [Multi-Subs][h264_qsv][8bit][AAC]",
			"BLEACH: Sennen Kessen-hen", 8},
		{"[Group] Some Show 12 [1080p][HEVC]", "Some Show", 12},
		{"Another Show 105 WEB-DL AAC", "Another Show", 105},
		// A version still rides along.
		{"[Group] Some Show 08v2 1080p", "Some Show", 8},

		// Not episodes: a year, a title that is itself a number, a single
		// digit, and a number with no quality run after it.
		{"[NoobSubs] your name. 2016 (1080p Blu-ray Dual Audio 8bit AC3)", "your name. 2016", 0},
		{"[Group] 86 1080p", "86", 0},
		{"[Group] Show 8 1080p", "Show 8", 0},
		{"[Group] Show 08 something else", "Show 08 something else", 0},
		// The separator is part of a number of its own.
		{"Evangelion.3.33.1080p.BluRay.x264", "Evangelion 3 33", 0},
		{"[Group] Show 5.1 1080p", "Show 5.1", 0},
	}
	for _, c := range cases {
		got := Parse(c.name)
		if got.Episode != c.episode {
			t.Errorf("%q: episode = %d, want %d", c.name, got.Episode, c.episode)
		}
		if got.Title != c.title {
			t.Errorf("%q: title = %q, want %q", c.name, got.Title, c.title)
		}
	}
}

// The version suffix is the one piece of the bare form worth keeping.
func TestBareNumberCarriesItsVersion(t *testing.T) {
	got := Parse("[Group] Some Show 08v2 1080p")
	if got.Version != 2 {
		t.Errorf("version = %d, want 2", got.Version)
	}
}
