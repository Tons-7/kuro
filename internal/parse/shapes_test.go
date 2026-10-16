package parse

import "testing"

// Names the parser used to misread: the episode title after the number, a span of the marked form, a
// number that is the show's name, and Chinese groups' bracketed numbering.
func TestEpisodeTitleAfterTheNumberIsNotTheShow(t *testing.T) {
	for name, want := range map[string]string{
		"[CBM]_Monster_-_02_-_Downfall_[78722558].mkv":                     "Monster",
		"[Group] Show - S01E05 - The Title [1080p].mkv":                    "Show",
		"[Group] Show - E05 - The Title [1080p].mkv":                       "Show",
		"[nekotan] Garden S01E01 Chapter 1: Thanatos (BD 1080p) | Kara no": "Garden | Kara no",
		"[Group] Steins;Gate 0 - 03 [1080p].mkv":                           "Steins;Gate 0",
	} {
		if got := Parse(name); got.Title != want {
			t.Errorf("%s: title %q, want %q", name, got.Title, want)
		}
	}
}

func TestMarkedSpanIsAPack(t *testing.T) {
	for _, name := range []string{"[Group] Show S01E01-E12 [1080p HEVC]", "Show.S01E01-12.1080p.WEB-DL-GRP"} {
		got := Parse(name)
		if got.Season != 1 || got.Episode != 1 || got.EpisodeEnd != 12 || !got.Batch {
			t.Errorf("%s: %+v", name, got)
		}
		if got.Title != "Show" {
			t.Errorf("%s: title %q", name, got.Title)
		}
	}
	if got := Parse("[Group] Show S01E05 [1080p]"); got.Episode != 5 || got.EpisodeEnd != 0 || got.Batch {
		t.Errorf("single: %+v", got)
	}
}

func TestNumberBeforeABracketIsTheName(t *testing.T) {
	for name, want := range map[string]struct {
		episode int
		title   string
	}{
		"[Group] Mob Psycho 100 [BD 1080p HEVC]": {0, "Mob Psycho 100"},
		"[Group] Cyborg 009 [BD 1080p]":          {0, "Cyborg 009"},
		"[Group] Show 08 [1080p]":                {8, "Show"},
		"[Group] Show 08 1080p":                  {8, "Show"},
		"[Group] Show 100 1080p":                 {100, "Show"},
	} {
		got := Parse(name)
		if got.Episode != want.episode || got.Title != want.title {
			t.Errorf("%s: episode %d title %q, want %d %q", name, got.Episode, got.Title, want.episode, want.title)
		}
	}
}

func TestBracketedNumberIsTheEpisode(t *testing.T) {
	for name, want := range map[string]struct {
		episode, version int
		title            string
	}{
		"[Nekomoe kissaten][Sousou no Frieren][05][1080p][JPSC]":   {5, 0, "Sousou no Frieren"},
		"[Sakurato] Sousou no Frieren [05v2][AVC-8bit 1080p][CHT]": {5, 2, "Sousou no Frieren"},
		"[VCB-Studio] Show [05][Ma10p_1080p][x265_flac].mkv":       {5, 0, "Show"},
		"[Group] Show (2024) [1080p]":                              {0, 0, "Show"},
	} {
		got := Parse(name)
		if got.Episode != want.episode || got.Version != want.version || got.Title != want.title {
			t.Errorf("%s: episode %d v%d title %q, want %d v%d %q", name, got.Episode, got.Version, got.Title,
				want.episode, want.version, want.title)
		}
	}
}

func TestBaseName(t *testing.T) {
	for in, want := range map[string]string{
		"Show/Show - 01 [1080p].mkv": "Show - 01 [1080p]",
		`Show\Show - 01.mkv`:         "Show - 01",
		"Show - 01.mkv":              "Show - 01",
		"noextension":                "noextension",
	} {
		if got := BaseName(in); got != want {
			t.Errorf("BaseName(%q) = %q, want %q", in, got, want)
		}
	}
	for _, name := range []string{"a.mkv", "b.OGM", "c.m4v", "d.wmv", "e.m2ts"} {
		if !IsVideo(name) {
			t.Errorf("%s is a video", name)
		}
	}
	if IsVideo("x.ass") || IsVideo("y.jpg") {
		t.Error("subtitles and artwork are not videos")
	}
}
