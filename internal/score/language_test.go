package score

import (
	"testing"

	"kuro/internal/indexer"
	"kuro/internal/parse"
)

func subCandidate(title string, seeders int) Candidate {
	return Candidate{
		Torrent:   indexer.Torrent{Title: title, Seeders: seeders, Size: 300 << 20, InfoHash: title},
		Release:   parse.Parse(title),
		Confirmed: true,
	}
}

// The adult title found thirteen releases, and the Chinese fansub group
// outscored the English one on quality and seeders alone.
func TestEnglishBeatsChineseHardsub(t *testing.T) {
	prefs := DefaultPreferences()

	english := subCandidate("[geckyzz] My Classmate's a Sexy Actress - S01E01 "+
		"(OVEIL.WEB-DL 1080P AVC, AAC, HARD SUB (EN))", 94)
	chinese := subCandidate("[桜都字幕组] 关于同组的染谷同学是性感女优这件事。 / "+
		"Onaji Zemi no Someya-san ga Sexy Joyuu Datta Hanashi. [01][1080P][简体内嵌]", 400)

	ranked := Rank([]Candidate{chinese, english}, prefs)
	if got := ranked[0].Torrent.Title; got != english.Torrent.Title {
		t.Errorf("ranked first:\n  %s\nwant the English release", got)
	}
}

func TestSubtitleLanguageDetection(t *testing.T) {
	cases := []struct {
		name    string
		want    []string
		hardSub bool
	}{
		{"[SubsPlease] Show - 01 (1080p) [ABCD1234]", nil, false},
		{"[Erai-raws] Show - 07 [1080p][Multiple Subtitle]", []string{"en"}, false},
		{"[Group] Show - 03 [1080p][English Sub]", []string{"en"}, false},
		{"[geckyzz] Show - S01E01 [HARD SUB (EN)]", []string{"en"}, true},
		{"[桜都字幕组] Show [01][1080P][简体内嵌]", []string{"zh"}, true},
		{"[Nekomoe kissaten] Show [01][1080p][简繁内封]", []string{"zh"}, false},
		{"[Group] Show - 05 [1080p][CHS]", []string{"zh"}, false},
		{"[Group] Show - 05 [VOSTFR]", []string{"fr"}, false},
		{"[Group] Show - 05 [Legendado PT-BR]", []string{"pt"}, false},
	}

	for _, c := range cases {
		rel := parse.Parse(c.name)
		if len(rel.Subtitles) != len(c.want) {
			t.Errorf("%s\n  subtitles = %v, want %v", c.name, rel.Subtitles, c.want)
			continue
		}
		for i, want := range c.want {
			if rel.Subtitles[i] != want {
				t.Errorf("%s\n  subtitles = %v, want %v", c.name, rel.Subtitles, c.want)
				break
			}
		}
		if rel.HardSub != c.hardSub {
			t.Errorf("%s\n  hardSub = %v, want %v", c.name, rel.HardSub, c.hardSub)
		}
	}
}

// Most English releases never say "English". Treating that silence as the
// wrong language would bury every one of them under the groups that do label.
func TestUnlabelledReleaseIsNotPenalised(t *testing.T) {
	prefs := DefaultPreferences()

	plain := subCandidate("[SubsPlease] Show - 01 (1080p) [ABCD1234]", 500)
	chinese := subCandidate("[Group] Show - 01 [1080p][简体内嵌]", 500)

	ranked := Rank([]Candidate{chinese, plain}, prefs)
	if ranked[0].Torrent.Title != plain.Torrent.Title {
		t.Errorf("ranked first: %s, want the unlabelled release", ranked[0].Torrent.Title)
	}
	for _, reason := range Rank([]Candidate{plain}, prefs)[0].Reasons {
		if reason == "subtitles only in zh" {
			t.Error("an unlabelled release was judged on language")
		}
	}
}

// The wrong language is a reason to rank lower, never a reason to refuse: for
// some shows it is the only release that exists.
func TestWrongLanguageStillPlayable(t *testing.T) {
	prefs := DefaultPreferences()
	only := subCandidate("[Group] Show - 01 [1080p][简体内嵌]", 120)

	ranked := Rank([]Candidate{only}, prefs)
	if !ranked[0].AutoPick {
		t.Errorf("blocked as %q; the wrong subtitles should rank down, not refuse", ranked[0].Blocked)
	}
}
