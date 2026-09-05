package library

import "testing"

// Trackers match tokens against a filename, and no filename contains "?!" or a
// trailing dot. Prowlarr's own Nyaa definition strips the same things.
func TestCleanQueryStripsWhatNoFilenameContains(t *testing.T) {
	cases := map[string]string{
		"My Classmate's a Sexy Actress, and Now We Live Together?!": "My Classmate s a Sexy Actress and Now We Live Together",
		"Onaji Zemi no Someya-san ga Sexy Joyuu Datta Hanashi.":     "Onaji Zemi no Someya-san ga Sexy Joyuu Datta Hanashi",
		"BLEACH: Thousand-Year Blood War":                           "BLEACH Thousand-Year Blood War",
		"Re:ZERO -Starting Life in Another World-":                  "Re ZERO -Starting Life in Another World",
	}

	for in, want := range cases {
		if got := cleanQuery(in); got != want {
			t.Errorf("cleanQuery(%q)\n  got  %q\n  want %q", in, got, want)
		}
	}
}

// A first season is almost never labelled as one in a release name, so keeping
// the marker only narrows the search to nothing.
func TestCleanQueryDropsSeasonOne(t *testing.T) {
	for _, in := range []string{
		"Some Show Season 1",
		"Some Show S1",
		"Some Show S01",
		"Some Show 1st Season",
	} {
		if got := cleanQuery(in); got != "Some Show" {
			t.Errorf("cleanQuery(%q) = %q", in, got)
		}
	}

	// Later seasons must survive: they are what separates them from season one.
	if got := cleanQuery("Some Show Season 2"); got != "Some Show Season 2" {
		t.Errorf("season two was stripped: %q", got)
	}
}

func TestSearchTermsAreClean(t *testing.T) {
	got := searchTerms(
		[]string{"Onaji Zemi no Someya-san ga Sexy Joyuu Datta Hanashi."},
		"My Classmate's a Sexy Actress, and Now We Live Together?!",
		4, 0, nil, false,
	)
	if len(got) == 0 {
		t.Fatal("no search terms")
	}
	for _, term := range got {
		for _, bad := range []string{"?", "!", ":", "'", "."} {
			if contains(term, bad) {
				t.Errorf("term %q still carries %q", term, bad)
			}
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
