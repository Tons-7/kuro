package match

import "testing"

func frierenIndex(t *testing.T) *Index {
	t.Helper()
	ix := NewIndex()

	// The real franchise, which is exactly where AniList's own search picks
	// the wrong entry.
	ix.Add(Media{
		ID: 154587, Format: "TV", Episodes: 28, Year: 2023, Season: 1,
		Titles: []string{
			"Sousou no Frieren",
			"Frieren: Beyond Journey's End",
			"葬送のフリーレン",
			"Frieren at the Funeral",
		},
	})
	ix.Add(Media{
		ID: 182255, Format: "TV", Episodes: 10, Year: 2026, Season: 2,
		Titles: []string{"Sousou no Frieren 2nd Season", "Frieren: Beyond Journey's End Season 2"},
	})
	ix.Add(Media{
		ID: 999001, Format: "ONA", Episodes: 1, Year: 2024,
		Titles: []string{"Sousou no Frieren: ●● no Mahou"},
	})
	ix.Add(Media{
		ID: 21, Format: "TV", Episodes: 0, Year: 1999,
		Titles: []string{"ONE PIECE", "One Piece"},
	})
	ix.Add(Media{
		ID: 16498, Format: "TV", Episodes: 25, Year: 2013,
		Titles: []string{"Shingeki no Kyojin", "Attack on Titan", "進撃の巨人", "SnK", "AoT"},
	})
	return ix
}

func TestMatchScenneNamesResolveCorrectly(t *testing.T) {
	ix := frierenIndex(t)

	tests := []struct {
		name  string
		query Query
		want  int
	}{
		{"romaji", Query{Title: "Sousou no Frieren"}, 154587},
		{"official english", Query{Title: "Frieren Beyond Journeys End"}, 154587},
		{"english with apostrophe", Query{Title: "Frieren: Beyond Journey's End"}, 154587},
		{"native", Query{Title: "葬送のフリーレン"}, 154587},
		{"second season", Query{Title: "Sousou no Frieren", Season: 2}, 182255},
		{"second season in title", Query{Title: "Sousou no Frieren 2nd Season"}, 182255},
		{"abbreviation", Query{Title: "AoT"}, 16498},
		{"english title", Query{Title: "Attack on Titan"}, 16498},
		{"case and spacing", Query{Title: "attack   on   titan"}, 16498},
		{"one piece", Query{Title: "One Piece"}, 21},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ix.Match(tt.query)
			if !ok {
				t.Fatalf("no match for %q", tt.query.Title)
			}
			if got.MediaID != tt.want {
				t.Fatalf("matched %d (%q, score %.2f), want %d",
					got.MediaID, got.Matched, got.Score, tt.want)
			}
		})
	}
}

// The exact failure that motivated building this: the full official English
// title must not land on a sibling franchise entry.
func TestFullEnglishTitleDoesNotDriftToSpinOff(t *testing.T) {
	ix := frierenIndex(t)

	for _, q := range []string{
		"Frieren Beyond Journey's End",
		"Frieren Beyond Journeys End",
		"Frieren.Beyond.Journeys.End",
	} {
		got, ok := ix.Match(Query{Title: q})
		if !ok {
			t.Fatalf("%q: no match", q)
		}
		if got.MediaID != 154587 {
			t.Fatalf("%q matched %d, want the main series", q, got.MediaID)
		}
	}
}

// A bare franchise name genuinely cannot be resolved, so it must be reported
// as ambiguous rather than guessed at.
func TestAmbiguousQueryIsFlagged(t *testing.T) {
	ix := frierenIndex(t)

	got, ok := ix.Match(Query{Title: "Frieren"})
	if !ok {
		t.Fatal("expected a candidate")
	}
	if got.Decision != Ask {
		t.Fatalf("matched %d with score %.2f margin %.2f; a bare franchise name should prompt",
			got.MediaID, got.Score, got.Margin)
	}
}

// A full, unambiguous title should clear the auto band so playback can start
// without interrupting the user.
func TestConfidentMatchIsAutomatic(t *testing.T) {
	ix := frierenIndex(t)

	for _, q := range []string{"Sousou no Frieren", "Attack on Titan", "Shingeki no Kyojin"} {
		got, ok := ix.Match(Query{Title: q})
		if !ok {
			t.Fatalf("%q: no match", q)
		}
		if got.Decision != Auto {
			t.Errorf("%q: score %.2f margin %.2f decided %q, want auto",
				q, got.Score, got.Margin, got.Decision)
		}
	}
}

func TestSeasonSeparatesFranchiseEntries(t *testing.T) {
	ix := frierenIndex(t)

	s1, _ := ix.Match(Query{Title: "Sousou no Frieren", Season: 1})
	s2, _ := ix.Match(Query{Title: "Sousou no Frieren", Season: 2})

	if s1.MediaID == s2.MediaID {
		t.Fatal("season did not separate the two entries")
	}
	if s1.MediaID != 154587 || s2.MediaID != 182255 {
		t.Fatalf("season 1 -> %d, season 2 -> %d", s1.MediaID, s2.MediaID)
	}
}

func TestYearBreaksTiesButCannotRescueAWeakTitle(t *testing.T) {
	ix := frierenIndex(t)

	// A correct year must not make an unrelated title match.
	if got, ok := ix.Match(Query{Title: "Completely Different Show", Year: 2023}); ok {
		t.Fatalf("matched %d on year alone", got.MediaID)
	}
}

func TestFormatMismatchIsPenalised(t *testing.T) {
	ix := frierenIndex(t)

	got, ok := ix.Match(Query{Title: "Sousou no Frieren", Format: "TV"})
	if !ok || got.MediaID != 154587 {
		t.Fatalf("got %+v", got)
	}
}

func TestNoMatchForUnknownTitles(t *testing.T) {
	ix := frierenIndex(t)

	for _, q := range []string{
		"Something That Does Not Exist At All",
		"zzzzzzzz",
		"",
		"   ",
	} {
		if got, ok := ix.Match(Query{Title: q}); ok {
			t.Errorf("%q matched %d with score %.2f", q, got.MediaID, got.Score)
		}
	}
}

// Every season of a franchise carries the same English title as a synonym, so
// the title alone cannot separate them. What the user is watching can.
func TestBoostSettlesFranchiseTies(t *testing.T) {
	ix := NewIndex()
	for _, id := range []int{16498, 20958, 99147, 104578} {
		ix.Add(Media{ID: id, Format: "TV", Episodes: 25, Year: 2013,
			Titles: []string{"Attack on Titan"}})
	}

	tied, _ := ix.Match(Query{Title: "Attack on Titan"})
	if tied.Decision != Ask {
		t.Fatalf("four identical titles should be ambiguous, got %q", tied.Decision)
	}

	// The user is midway through one of them.
	got, ok := ix.Match(Query{
		Title: "Attack on Titan",
		Boost: map[int]float64{99147: BoostWatching, 16498: BoostCompleted},
	})
	if !ok {
		t.Fatal("no match")
	}
	if got.MediaID != 99147 {
		t.Fatalf("matched %d, want the season being watched", got.MediaID)
	}
	if got.Decision != Auto {
		t.Fatalf("decision = %q with margin %.1f, want auto", got.Decision, got.Margin)
	}
}

// A boost must settle a tie, never overturn a genuinely better title match.
func TestBoostCannotOverrideABetterTitle(t *testing.T) {
	ix := NewIndex()
	ix.Add(Media{ID: 1, Titles: []string{"Sousou no Frieren"}})
	ix.Add(Media{ID: 2, Titles: []string{"Completely Unrelated Show"}})

	got, ok := ix.Match(Query{
		Title: "Sousou no Frieren",
		Boost: map[int]float64{2: BoostWatching},
	})
	if !ok || got.MediaID != 1 {
		t.Fatalf("matched %d; a boost outranked the correct title", got.MediaID)
	}
}

// An episode beyond a season's length rules that season out.
func TestEpisodeOutOfRangeIsPenalised(t *testing.T) {
	ix := NewIndex()
	ix.Add(Media{ID: 1, Episodes: 12, Titles: []string{"Show"}})
	ix.Add(Media{ID: 2, Episodes: 24, Titles: []string{"Show"}})

	got, ok := ix.Match(Query{Title: "Show", Episode: 20})
	if !ok || got.MediaID != 2 {
		t.Fatalf("matched %d, want the entry long enough to contain episode 20", got.MediaID)
	}
}

func TestEveryMatchExplainsItself(t *testing.T) {
	ix := frierenIndex(t)

	got, ok := ix.Match(Query{Title: "Attack on Titan"})
	if !ok {
		t.Fatal("no match")
	}
	if len(got.Reasons) == 0 {
		t.Fatal("no reasons recorded")
	}
	if got.Matched == "" {
		t.Fatal("did not report which title matched")
	}
}

func TestIndexSkipsEmptyMedia(t *testing.T) {
	ix := NewIndex()
	ix.Add(Media{ID: 1})
	ix.Add(Media{ID: 2, Titles: []string{"", "   "}})

	if ix.Len() != 0 {
		t.Fatalf("indexed %d entries with no usable titles", ix.Len())
	}
}

func TestDice(t *testing.T) {
	tests := []struct {
		a, b string
		min  float64
		max  float64
	}{
		{"frieren", "frieren", 1, 1},
		{"frieren", "frieren beyond", 0.4, 0.8},
		{"a", "a", 1, 1},
		{"", "", 1, 1},
		{"ab", "", 0, 0},
	}

	for _, tt := range tests {
		if got := dice(tt.a, tt.b); got < tt.min || got > tt.max {
			t.Errorf("dice(%q, %q) = %.3f, want between %.2f and %.2f", tt.a, tt.b, got, tt.min, tt.max)
		}
	}
}

// Unrelated strings still share common bigrams, so the absolute value matters
// less than staying clear of the thresholds that produce a match.
func TestDiceKeepsUnrelatedTitlesBelowThreshold(t *testing.T) {
	pairs := [][2]string{
		{"frieren", "completely different"},
		{"attack on titan", "one piece"},
		{"bocchi the rock", "sousou no frieren"},
	}

	for _, p := range pairs {
		if got := dice(p[0], p[1]); got >= thresholdFuzzyMedium {
			t.Errorf("dice(%q, %q) = %.3f, at or above the %.2f match threshold",
				p[0], p[1], got, thresholdFuzzyMedium)
		}
	}
}

func TestDiceIsSymmetric(t *testing.T) {
	pairs := [][2]string{
		{"sousou no frieren", "frieren beyond journeys end"},
		{"attack on titan", "shingeki no kyojin"},
	}
	for _, p := range pairs {
		if a, b := dice(p[0], p[1]), dice(p[1], p[0]); a != b {
			t.Errorf("dice(%q,%q)=%.4f but reversed=%.4f", p[0], p[1], a, b)
		}
	}
}
