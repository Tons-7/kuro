package metadata

import "testing"

// The upstream ships a theme as one line, and titles contain quotes and
// brackets of their own, so the split cannot be taken at the first match.
func TestParseTheme(t *testing.T) {
	for _, tc := range []struct{ in, title, artist, eps string }{
		{`1: "Yuusha (勇者)" by YOASOBI (eps 1-16)`, "Yuusha (勇者)", "YOASOBI", "1-16"},
		{`2: "Haru (晴る)" by Yorushika (ヨルシカ) (eps 17-28)`, "Haru (晴る)", "Yorushika (ヨルシカ)", "17-28"},
		{`S1: "bliss" by milet (eps Special Broadcast: 1-4)`, "bliss", "milet", "Special Broadcast: 1-4"},
		// A title carrying its own quote: the split is the last one, not the first.
		{`1: "Step - Step" by Ziggy`, "Step - Step", "Ziggy", ""},
		{`"Anytime Anywhere" by milet`, "Anytime Anywhere", "milet", ""},
		// Nothing to split on is not a parse failure; the line is the answer.
		{`3: "A title with no artist"`, "A title with no artist", "", ""},
	} {
		got := parseTheme(tc.in)
		if got.Title != tc.title || got.Artist != tc.artist || got.Episodes != tc.eps {
			t.Errorf("parseTheme(%q)\n got  %+v\n want title %q artist %q eps %q",
				tc.in, got, tc.title, tc.artist, tc.eps)
		}
	}
}
