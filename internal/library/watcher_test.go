package library

import (
	"strings"
	"testing"

	"kuro/internal/parse"
	"kuro/internal/store"
)

// A dual-audio release satisfies both preferences: both tracks are in the file
// and the choice happens at playback.
func TestAudioFilter(t *testing.T) {
	sub := parse.Parse("[SubsPlease] Show - 07 (1080p) [ABCD1234].mkv")
	dual := parse.Parse("[Group] Show - 07 [1080p][Dual Audio].mkv")
	dubOnly := parse.Parse("[Group] Show - 07 [1080p][English Dub].mkv")

	tests := []struct {
		pref                       string
		wantSub, wantDual, wantDub bool
	}{
		{"sub", true, true, false},
		{"dub", false, true, true},
		{"both", true, true, true},
		{"either", true, true, true},
		{"", true, true, false},
	}

	for _, tt := range tests {
		t.Run(tt.pref, func(t *testing.T) {
			f := audioFilter(tt.pref)
			if got := f(sub); got != tt.wantSub {
				t.Errorf("subbed release = %v, want %v", got, tt.wantSub)
			}
			if got := f(dual); got != tt.wantDual {
				t.Errorf("dual audio = %v, want %v", got, tt.wantDual)
			}
			if got := f(dubOnly); got != tt.wantDub {
				t.Errorf("dub only = %v, want %v", got, tt.wantDub)
			}
		})
	}
}

// mentionsDub only spots an explicit dub label. Dual audio is a separate
// signal the parser already extracts, and the filter combines the two.
func TestMentionsDub(t *testing.T) {
	tests := map[string]bool{
		"[Group] Show - 07 [English Dub]": true,
		"[Group] Show - 07 [Dubbed]":      true,
		"[Group] Show - 07 [Dual Audio]":  false,
		"[Group] Show - 07 [1080p]":       false,
		"[SubsPlease] Show - 07 (1080p)":  false,
	}
	for name, want := range tests {
		if got := mentionsDub(parse.Parse(name)); got != want {
			t.Errorf("%q = %v, want %v", name, got, want)
		}
	}
}

func TestSearchTermsLeadWithEpisodeNumber(t *testing.T) {
	titles := []string{"Sousou no Frieren", "Frieren: Beyond Journey's End", "Фрірен", "葬送のフリーレン"}

	got := searchTerms(titles, "", 10, 0, nil, false)
	if len(got) == 0 {
		t.Fatal("no search terms produced")
	}
	// Results are ranked by seeders, and a season batch always out-seeds a
	// single episode, so the number has to be in the first query.
	if got[0] != "Sousou no Frieren 10" {
		t.Errorf("first term = %q, want the episode number included", got[0])
	}
	// A tracker search for a Cyrillic or CJK title returns nothing and only
	// burns a rate-limited request.
	for _, term := range got {
		if !mostlyLatin(term) {
			t.Errorf("non-Latin search term %q", term)
		}
	}
	if len(got) > maxSearchTerms {
		t.Errorf("%d terms; each one costs a tracker request", len(got))
	}
}

// Release groups split by naming convention, so romaji-only search can miss the
// healthy releases posted under the English name.
func TestSearchTermsPairEpisodeWithBothNamings(t *testing.T) {
	titles := []string{
		"Bleach: Sennen Kessen-hen - Kashin-tan",
		"BLEACH TYBW",
		"Bleach: Sennen Kessen-hen Part 4",
	}
	english := "BLEACH: Thousand-Year Blood War - The Calamity"

	got := searchTerms(titles, english, 41, 0, nil, false)

	// Punctuation is stripped before searching: it never appears in a release
	// filename, and trackers match on tokens.
	var romajiWithEp, englishWithEp bool
	for _, term := range got {
		if term == "Bleach Sennen Kessen-hen - Kashin-tan 41" {
			romajiWithEp = true
		}
		if term == "BLEACH Thousand-Year Blood War - The Calamity 41" {
			englishWithEp = true
		}
	}
	if !romajiWithEp {
		t.Errorf("romaji title never paired with the episode: %v", got)
	}
	if !englishWithEp {
		t.Errorf("English title never paired with the episode: %v", got)
	}
}

// The English title is often the romaji, and a duplicate query is a wasted
// request.
func TestSearchTermsSkipsRedundantEnglish(t *testing.T) {
	got := searchTerms([]string{"Dandadan"}, "Dandadan", 4, 0, nil, false)

	seen := map[string]int{}
	for _, term := range got {
		seen[term]++
		if seen[term] > 1 {
			t.Fatalf("term %q searched twice: %v", term, got)
		}
	}
}

func TestSearchTermsWithoutEpisode(t *testing.T) {
	got := searchTerms([]string{"Some Show"}, "", 0, 0, nil, false)
	if len(got) != 1 || got[0] != "Some Show" {
		t.Fatalf("got %v", got)
	}
}

func TestSearchTermsAllNonLatin(t *testing.T) {
	if got := searchTerms([]string{"葬送のフリーレン", "", "Фрірен"}, "", 5, 0, nil, false); len(got) != 0 {
		t.Fatalf("got %v, want nothing searchable", got)
	}
}

// A generic title search ranks by seeders and the curated release is often not
// the most-seeded, so it has to be asked for by name.
func TestSearchTermsLeadWithCuratedGroup(t *testing.T) {
	got := searchTerms([]string{"Sousou no Frieren"}, "", 10, 0, []string{"PMR"}, false)
	if len(got) == 0 {
		t.Fatal("no terms")
	}
	if got[0] != "PMR Sousou no Frieren" {
		t.Fatalf("first term = %q, want the curated group first", got[0])
	}
	// The episode-number query must still follow as the fallback.
	if len(got) < 2 || got[1] != "Sousou no Frieren 10" {
		t.Fatalf("second term = %v", got)
	}
}

// A curated release may use a different title, and the shortest title is the
// safest substring to pair the group with.
func TestCuratedGroupPairsWithShortestTitle(t *testing.T) {
	titles := []string{"Sousou no Frieren", "Frieren", "Frieren Beyond Journeys End"}

	got := searchTerms(titles, "", 10, 0, []string{"PMR"}, false)
	if got[0] != "PMR Frieren" {
		t.Fatalf("first term = %q, want the group paired with the shortest title", got[0])
	}
}

func TestSearchTermsIgnoresUnusableGroup(t *testing.T) {
	got := searchTerms([]string{"Some Show"}, "", 3, 0, []string{"", "  "}, false)
	if len(got) == 0 || got[0] != "Some Show 03" {
		t.Fatalf("got %v; a blank group should be skipped", got)
	}
}

func TestMostlyLatin(t *testing.T) {
	tests := map[string]bool{
		"Sousou no Frieren": true,
		"Frieren: Beyond":   true,
		"Re:Zero 2":         true,
		"葬送のフリーレン":          false,
		"Фрірен":            false,
		"":                  true,
	}
	for in, want := range tests {
		if got := mostlyLatin(in); got != want {
			t.Errorf("mostlyLatin(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestVerifiesRejectsWrongEpisodeAndSeason(t *testing.T) {
	req := Request{Episode: 10, Season: 2}

	tests := []struct {
		name string
		file string
		want bool
	}{
		{"right episode and season", "[G] Show S2 - 10 [1080p].mkv", true},
		{"wrong episode", "[G] Show S2 - 09 [1080p].mkv", false},
		{"wrong season", "[G] Show S3 - 10 [1080p].mkv", false},
		{"no season stated", "[G] Show - 10 [1080p].mkv", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := verifies(parse.Parse(tt.file), req); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// Attack on Titan's Final Season Part 2: its own episode 1, 17 counted through
// the season. Cours restart numbering, so a Part 3 episode 1 is not the Part 2
// episode 1 asked for; season alone can't separate them.
func part2Request() Request {
	return Request{
		Episode: 1, Season: 1, Part: 2, Alias: store.EpisodeAlias{Tvdb: 17},
		Cour: Cour{Later: true, Bases: [][]string{{"shingeki", "kyojin", "final"}}},
	}
}

func TestVerifiesRejectsWrongPart(t *testing.T) {
	req := part2Request()

	tests := []struct {
		name string
		file string
		want bool
	}{
		{"same part, cour numbering", "[Erai-raws] Shingeki no Kyojin - The Final Season Part 2 - 01 [1080p].mkv", true},
		{"other part, cour numbering", "[ASW] Shingeki no Kyojin - The Final Season Part 3 - 01 [1080p].mkv", false},
		{"same part, season numbering", "[G] Shingeki no Kyojin Part 2 - 17 [1080p].mkv", true},
		{"no part stated, season numbering", "[G] Shingeki no Kyojin - 17 [1080p].mkv", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := verifies(parse.Parse(tt.file), req); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// The cour's own numbering is only safe with a part marker agreeing. Otherwise
// "Attack on Titan - 01" is season one, not the first episode of a later cour.
func TestVerifiesOwnNumberNeedsAPartMarker(t *testing.T) {
	req := part2Request()

	if verifies(parse.Parse("[G] Shingeki no Kyojin - 01 [1080p].mkv"), req) {
		t.Error("an unmarked episode 1 must not satisfy a Part 2 request")
	}
	// Inside an unmarked pack the file is only found by the season's count.
	if got := numbersFor(parse.Parse("[G] Shingeki no Kyojin [Batch].mkv"), req); len(got) != 1 || got[0] != 17 {
		t.Errorf("unmarked pack offers %v, want [17]", got)
	}
	if got := numbersFor(parse.Parse("[G] Shingeki no Kyojin Part 2 [Batch].mkv"), req); len(got) != 2 || got[0] != 1 {
		t.Errorf("marked pack offers %v, want [1 17]", got)
	}
}

// Both numberings are searched, because which one a group writes is not
// predictable from the show.
func TestSearchTermsIncludeBothNumberings(t *testing.T) {
	got := searchTerms([]string{"Shingeki no Kyojin: The Final Season Part 2"}, "", 1, 17, nil, false)

	var catalogue, cour bool
	for _, term := range got {
		if strings.HasSuffix(term, " 17") {
			catalogue = true
		}
		if strings.HasSuffix(term, " 01") {
			cour = true
		}
	}
	if !catalogue || !cour {
		t.Errorf("catalogue=%v cour=%v in %v", catalogue, cour, got)
	}
}

// A dub is a handful of releases among hundreds, so it is asked for by name;
// the sub search is unchanged.
func TestSearchTermsAskForTheDubByName(t *testing.T) {
	sub := searchTerms([]string{"Jujutsu Kaisen"}, "", 5, 0, nil, false)
	for _, term := range sub {
		if strings.Contains(strings.ToLower(term), "dub") {
			t.Errorf("a sub search should not mention dub: %v", sub)
		}
	}

	dub := searchTerms([]string{"Jujutsu Kaisen"}, "", 5, 0, nil, true)
	var named bool
	for _, term := range dub {
		if strings.HasSuffix(strings.ToLower(term), " dub") {
			named = true
		}
	}
	if !named {
		t.Errorf("a dub search should ask for the dub by name: %v", dub)
	}
}

// Most single-season shows never mention a season, so only an explicitly
// higher one contradicts a season-1 request.
func TestVerifiesSeasonOneAllowsUnmarked(t *testing.T) {
	req := Request{Episode: 5, Season: 1}

	if !verifies(parse.Parse("[G] Show - 05 [1080p].mkv"), req) {
		t.Error("an unmarked release should satisfy a season 1 request")
	}
	if verifies(parse.Parse("[G] Show S2 - 05 [1080p].mkv"), req) {
		t.Error("a season 2 release should not satisfy a season 1 request")
	}
}
