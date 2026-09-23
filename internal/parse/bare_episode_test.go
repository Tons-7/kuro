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

// The 264 of "H.264" is a codec, not episode 264: packs and films named that way
// vanished from results.
func TestCodecIsNotABareEpisode(t *testing.T) {
	for _, name := range []string{
		"[FLE] The Apothecary Diaries - S02 (BD Remux 1080p H.264 FLAC) [Dual Audio]",
		"Show.S01.1080p.BluRay.H.264.FLAC-GRP",
		"[Group] Kimi no Na wa. (BD 1080p H.265 10bit Opus)",
		"[Group] Show [BD 720p x.264 AC3]",
	} {
		if got := Parse(name); got.Episode != 0 {
			t.Errorf("%q: episode = %d, want none", name, got.Episode)
		}
	}
}

// A number before " - EE" is the title's or the season's, not the start of a
// range: these are single episodes.
func TestTitleNumberBeforeTheEpisodeIsNotARange(t *testing.T) {
	cases := []struct {
		name    string
		episode int
	}{
		{"[Erai-raws] Spy x Family Season 2 - 05 [1080p]", 5},
		{"[SubsPlease] Kono Subarashii Sekai ni Shukufuku wo! 3 - 05 (1080p)", 5},
		{"[Anime Time] Kaiju No. 8 - 09 [1080p]", 9},
		{"[Doomdos] - Hana-Kimi Season 2 - 19 [2160p IQ WEB-DL]", 19},
	}
	for _, c := range cases {
		got := Parse(c.name)
		if got.Episode != c.episode || got.EpisodeEnd != 0 || got.Batch {
			t.Errorf("%q: %d-%d batch=%v, want episode %d", c.name, got.Episode, got.EpisodeEnd, got.Batch, c.episode)
		}
	}
	// Real spans still read as ranges.
	for _, name := range []string{
		"[Group] Bocchi the Rock! - 01 ~ 12 [1080p]",
		"[SubsPlease] Show (01-12) (1080p) [Batch]",
		"[Group] Show Season 2 (01-12) [Batch]",
	} {
		if got := Parse(name); !got.Batch || got.EpisodeEnd != 12 {
			t.Errorf("%q: %d-%d batch=%v, want a 12-episode span", name, got.Episode, got.EpisodeEnd, got.Batch)
		}
	}
}

func TestResolutionGluedToTheSource(t *testing.T) {
	for name, source := range map[string]string{
		"[Group] Show - 05 [BD1080p]":   "BD",
		"[Group] Show - 05 [WEB1080p]":  "WEB",
		"[Group] Show - 05 [BD_1080p]":  "BD",
		"[Group] Show - 05 [WEB_1080p]": "WEB",
	} {
		got := Parse(name)
		if got.Resolution != "1080p" || got.Source != source {
			t.Errorf("%q: resolution %q source %q, want 1080p %s", name, got.Resolution, got.Source, source)
		}
	}
}

// A finale marker belongs to the episode, not the title, or short titles fail
// the show check.
func TestFinaleMarkerLeavesTheTitle(t *testing.T) {
	for _, name := range []string{
		"[Erai-raws] Oshi no Ko - 12 END [1080p]",
		"[Erai-raws] Oshi no Ko - 12 FINAL [1080p]",
	} {
		if got := Parse(name); got.Title != "Oshi no Ko" || got.Episode != 12 {
			t.Errorf("%q: title %q episode %d", name, got.Title, got.Episode)
		}
	}
	if got := Parse("[Group] Seraph of the End - 05 [1080p]"); got.Title != "Seraph of the End" {
		t.Errorf("a title ending in End lost it: %q", got.Title)
	}
}

func TestNumeralSeason(t *testing.T) {
	cases := []struct {
		title string
		n     int
		roman bool
	}{
		{"Overlord IV", 4, true},
		{"Mob Psycho 100 III", 3, true},
		{"Mushoku Tensei II: Isekai Ittara Honki Dasu", 2, true},
		{"Kono Subarashii Sekai ni Shukufuku wo! 3", 3, false},
		{"Kaiju No. 8", 8, false},
		{"Sousou no Frieren", 0, false},
		{"Mob Psycho 100", 0, false},
	}
	for _, c := range cases {
		if n, roman := NumeralSeason(c.title); n != c.n || roman != c.roman {
			t.Errorf("%q: %d %v, want %d %v", c.title, n, roman, c.n, c.roman)
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
