package library

import (
	"testing"

	"kuro/internal/corpus"
	"kuro/internal/parse"
)

func identifies(t *testing.T, releaseTitle string, known ...string) bool {
	t.Helper()
	return identifiesShow(parse.Parse(releaseTitle), releaseTitle, known)
}

// Monster is one common word, so every search for it returned other shows and
// the scorer preferred whichever of them was newest and highest resolution.
func TestMonsterOnlyMatchesMonster(t *testing.T) {
	known := []string{"MONSTER", "Monster", "Potwór"}

	wrong := []string{
		"[Erai-raws] Monogatari Series - Off and Monster Season - 01 [1080p][Multiple Subtitle]",
		"[SubsPlease] Monogatari Series - Off & Monster Season - 01 (1080p) [196CB8E2].mkv",
		"[Erai-raws] Re-Monster - 01 [1080p][Multiple Subtitle]",
		"[SanKyuu] Re Monster - 01 [WEB 1080p AV1 AAC][Multi-Sub]",
		"[Erai-raws] Monster Musume no Oishasan - 01 [1080p][Multiple Subtitle].mkv",
		"[HorribleSubs] Monster Musume no Iru Nichijou - 01 [1080p].mkv",
		"[ToonsHub] Monster Eater S01E01 1080p AMZN WEB-DL DDP2.0 H.264",
		"[HorribleSubs] Monster Hunter Stories Ride On - 01 [1080p].mkv",
		"[HorribleSubs] Monster Strike 2 - 01 [1080p].mkv",
		"[somedroplet] Pokémon Horizons / Pocket Monsters (2023) - 030",
		"[flowernal] Pocket Monsters (2023) - 074v2 [1080p] [HEVC]",
		"[EMBER] Monster Girl Doctor (2020) (Season 1) [BDRip] [1080p Dual Audio HEVC]",
		"[Beltraz] Beheneko: The Elf-Girl's Cat is Secretly an S-Ranked Monster! (2025)",
		"[GX_ST]Yu-Gi-Oh Duel Monsters Remastered 21-30 Batch",
	}
	for _, title := range wrong {
		if identifies(t, title, known...) {
			t.Errorf("accepted a different show: %s", title)
		}
	}

	right := []string{
		"[sam] Monster (2004) - 01 (DVD 572p HEVC x265 10-bit AC-3) [Dual-Audio]",
		"Monster.2004.S01.1080p.BluRay.DUAL.FLAC.2.0.x264-Kitsune",
		"[CBM] Monster 1-74 Complete (Dual Audio) [DVDRip-480p-8bit]",
		"[BlackRabbit] Monster (2004) - S01 [Bluray-720p][Opus 2.0][Dual Audio][AV1]",
		"[Anime Time] Monster (2004 - 2005) Complete [Dual Audio] [DVD][480p][Batch]",
		"Monster S01 MULTi 480p NF WEB-DL AAC2.0 x264-Tsundere-Raws",
	}
	for _, title := range right {
		if !identifies(t, title, known...) {
			t.Errorf("rejected the show itself: %s", title)
		}
	}
}

// The group spells it "Semi", the catalogue "Zemi": one word apart is still the
// same show, which a strict comparison would lose.
func TestRomanisationDifferenceStillMatches(t *testing.T) {
	known := []string{
		"Onaji Zemi no Someya-san ga Sexy Joyuu Datta Hanashi.",
		"My Classmate's a Sexy Actress, and Now We Live Together?!",
	}

	right := []string{
		"[geckyzz] My Classmate's a Sexy Actress, and Now We Live Together! - S01E04 " +
			"(Onaji Semi no Someya-san ga Sexy Joyuu datta Hanashi.) [1080P]",
		"[Group] Onaji Semi no Someya-san ga Sexy Joyuu Datta Hanashi - 04 [1080p].mkv",
	}
	for _, title := range right {
		if !identifies(t, title, known...) {
			t.Errorf("rejected over a spelling difference: %s", title)
		}
	}

	wrong := "[SubsPlease] Ponkotsu Fuuki Iin to Skirt-take ga Futekisetsu na JK no Hanashi - 04 (1080p).mkv"
	if identifies(t, wrong, known...) {
		t.Error("a different show sharing one common word was accepted")
	}
}

func TestShortenedTitlesMatch(t *testing.T) {
	long := "Kaguya-sama wa Kokurasetai: Tensai-tachi no Renai Zunousen"

	if !identifies(t, "[Group] Kaguya-sama wa Kokurasetai - 05 [1080p].mkv", long) {
		t.Error("a group dropping the subtitle should still match")
	}
	// The same rule must not run backwards: naming most of a long title is a
	// shortening, naming one word of it is another show.
	if identifies(t, "[Group] Kaguya - 05 [1080p].mkv", long) {
		t.Error("one leading word is not enough to claim a long title")
	}
	if identifies(t, "[Group] Monster - 05 [1080p].mkv", "Monster Musume no Oisha-san") {
		t.Error("a short title must not claim a longer show that starts with it")
	}
}

func TestSeasonAndYearAreNotIdentity(t *testing.T) {
	if !identifies(t, "[Group] Boku no Hero Academia 7th Season - 03 [1080p].mkv", "Boku no Hero Academia") {
		t.Error("a season marker should not make it a different show")
	}
	if !identifies(t, "[Group] Yuru Camp Season 2 - 03 [1080p].mkv", "Yuru Camp△") {
		t.Error("a stated season should not break the match")
	}
	if !identifies(t, "Monster.2004.S01.1080p.BluRay-Kitsune", "Monster") {
		t.Error("a year should not make it a different show")
	}
}

// Adding words to a one-word title is another show.
func TestSiblingEntriesAreNotAccepted(t *testing.T) {
	pairs := []struct{ release, known string }{
		{"[Group] Steins;Gate 0 - 03 [1080p].mkv", "Steins;Gate"},
		{"[Group] Bleach: Sennen Kessen-hen - 03 [1080p].mkv", "Bleach"},
		{"[Group] Fate/Apocrypha - 03 [1080p].mkv", "Fate/Zero"},
	}
	for _, p := range pairs {
		if identifies(t, p.release, p.known) {
			t.Errorf("%q was accepted as %q", p.release, p.known)
		}
	}
}

// A sequel's catalogue title carries a subtitle groups never use; they name
// the base and count seasons. Both used to be blocked as other shows.
func TestSequelsNamedByTheirBaseTitleMatch(t *testing.T) {
	pairs := []struct{ release, known string }{
		{"[Judas] Made in Abyss (Season 2) (BD 1080p)", "Made in Abyss: Retsujitsu no Ougonkyou"},
		{"[SubsPlease] Mushoku Tensei S2 - 05 (1080p).mkv", "Mushoku Tensei II: Isekai Ittara Honki Dasu"},
		{"[SubsPlease] Shingeki no Kyojin - The Final Season Part 3 - 01 (1080p).mkv",
			"Shingeki no Kyojin: The Final Season - Kanketsu-hen"},
		{"[Group] Sousou no Frieren - 03 [1080p].mkv", "Sousou no Frieren: Second Season"},
	}
	for _, p := range pairs {
		if !identifies(t, p.release, p.known) {
			t.Errorf("%q was rejected for %q", p.release, p.known)
		}
	}
	// A base of one word gives no such licence; the sequel logic elsewhere
	// tells cours apart, this only has to keep other shows out.
	if identifies(t, "[Group] Monster Musume no Oisha-san - 01 [1080p].mkv", "Monster: Season 1") {
		t.Error("a one-word base accepted a longer show")
	}
}

func TestBaseOf(t *testing.T) {
	tests := map[string]string{
		"Made in Abyss: Retsujitsu no Ougonkyou":            "Made in Abyss",
		"Shingeki no Kyojin: The Final Season - Kanketsu-hen": "Shingeki no Kyojin",
		"Bleach - Sennen Kessen-hen":                         "Bleach",
		"Monster":                                            "Monster",
		": odd":                                              ": odd",
	}
	for in, want := range tests {
		if got := baseOf(in); got != want {
			t.Errorf("baseOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAnyKnownTitleMatches(t *testing.T) {
	known := []string{"Sousou no Frieren", "Frieren: Beyond Journey's End"}

	for _, title := range []string{
		"[SubsPlease] Sousou no Frieren - 10 (1080p) [7D35515E].mkv",
		"[Group] Frieren - Beyond Journey's End - 10 [1080p].mkv",
		"[Group] Sōsō no Frieren - 10 [1080p].mkv",
	} {
		if !identifies(t, title, known...) {
			t.Errorf("rejected a known naming: %s", title)
		}
	}
}

func TestEmptyInputs(t *testing.T) {
	if identifies(t, "[Group] Show - 01 [1080p].mkv") {
		t.Error("with no known titles nothing can be identified")
	}
	if identifies(t, "", "Monster") {
		t.Error("an empty release name identifies nothing")
	}
	if identifies(t, "[Group] Show - 01.mkv", "", "   ") {
		t.Error("blank known titles must not match")
	}
}

func TestOverlap(t *testing.T) {
	tests := []struct {
		a, b []string
		want float64
	}{
		{[]string{"monster"}, []string{"monster"}, 1},
		{[]string{"re", "monster"}, []string{"monster"}, 0.5},
		{[]string{"a", "b", "c"}, []string{"a", "b", "d"}, 0.5},
		{nil, []string{"a"}, 0},
		{nil, nil, 0},
	}
	for _, tt := range tests {
		if got := overlap(tt.a, tt.b); got != tt.want {
			t.Errorf("overlap(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestIdentityTokens(t *testing.T) {
	tests := map[string][]string{
		"Monster (2004)":            {"monster"},
		"Monster 2004":              {"monster"},
		"Boku no Hero Academia S2":  {"boku", "no", "hero", "academia"},
		"Yuru Camp Season 2":        {"yuru", "camp"},
		"Steins;Gate 0":             {"steins", "gate", "0"},
		"Show 1999 Season 3 Part 1": {"show"},
		"":                          {},
	}
	for in, want := range tests {
		got := identityTokens(corpus.Normalise(in))
		if len(got) != len(want) {
			t.Errorf("identityTokens(%q) = %v, want %v", in, got, want)
			continue
		}
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("identityTokens(%q) = %v, want %v", in, got, want)
				break
			}
		}
	}
}
