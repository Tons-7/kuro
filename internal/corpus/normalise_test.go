package corpus

import (
	"sync"
	"testing"
)

// Normalise handles everything that is unambiguously the same title written
// differently: case, punctuation, apostrophes, spacing.
func TestNormaliseEquivalentSpellings(t *testing.T) {
	groups := [][]string{
		{"Jujutsu Kaisen", "JUJUTSU KAISEN", "jujutsu  kaisen"},
		{"Fate/Zero", "Fate Zero", "Fate / Zero"},
		{"Re:Zero kara Hajimeru Isekai Seikatsu", "Re Zero kara Hajimeru Isekai Seikatsu"},
		{"Frieren: Beyond Journey's End", "Frieren Beyond Journeys End", "Frieren - Beyond Journey's End"},
		{"K-On!", "K On"},
	}

	for _, variants := range groups {
		want := Normalise(variants[0])
		for _, v := range variants[1:] {
			if got := Normalise(v); got != want {
				t.Errorf("%q -> %q, but %q -> %q", variants[0], want, v, got)
			}
		}
	}
}

// Fold reconciles the three ways a long vowel gets romanised. It is lossy, so
// it is only ever consulted after an exact Normalise match fails.
func TestFoldReconcilesRomanisation(t *testing.T) {
	groups := [][]string{
		{"Sousou no Frieren", "Sōsō no Frieren", "Soosoo no Frieren"},
		{"Yuugi", "Yūgi", "Yugi"},
		{"Ookami", "Ōkami", "Okami"},
		{"Juujutsu Kaisen", "Jūjutsu Kaisen"},
	}

	for _, variants := range groups {
		want := Fold(variants[0])
		for _, v := range variants[1:] {
			if got := Fold(v); got != want {
				t.Errorf("Fold(%q) = %q, but Fold(%q) = %q", variants[0], want, v, got)
			}
		}
	}
}

// Folding must not undo what Normalise already settled.
func TestFoldKeepsSeasonsDistinct(t *testing.T) {
	if Fold("Show Season 1") == Fold("Show Season 2") {
		t.Fatal("seasons collapsed together")
	}
	if Fold("Monogatari") == Fold("Nisemonogatari") {
		t.Fatal("distinct titles collapsed")
	}
}

func TestNormaliseSeasonForms(t *testing.T) {
	want := Normalise("Show Season 2")
	for _, v := range []string{"Show 2nd Season", "Show S2", "Show Part 2", "Show Cour 2"} {
		if got := Normalise(v); got != want {
			t.Errorf("%q -> %q, want %q", v, got, want)
		}
	}
}

func TestNormaliseRomanNumerals(t *testing.T) {
	if Normalise("Show II") != Normalise("Show 2") {
		t.Errorf("II -> %q, 2 -> %q", Normalise("Show II"), Normalise("Show 2"))
	}
	if Normalise("Show III") != Normalise("Show 3") {
		t.Errorf("III -> %q, 3 -> %q", Normalise("Show III"), Normalise("Show 3"))
	}
}

func TestNormaliseKeepsDistinctTitlesDistinct(t *testing.T) {
	// Over-normalising is worse than under-normalising: collapsing two real
	// shows into one form makes the wrong pick unavoidable.
	distinct := [][2]string{
		{"Steins;Gate", "Steins;Gate 0"},
		{"Show Season 1", "Show Season 2"},
		{"Monogatari", "Nisemonogatari"},
		{"Code Geass", "Code Geass R2"},
	}

	for _, pair := range distinct {
		if Normalise(pair[0]) == Normalise(pair[1]) {
			t.Errorf("%q and %q collapsed to the same form %q", pair[0], pair[1], Normalise(pair[0]))
		}
	}
}

func TestNormaliseHandlesJapaneseAndEdgeCases(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", ""},
		{"   ", ""},
		{"!!!", ""},
		{"  Multiple   Spaces  ", "multiple spaces"},
		{"Fullwidth　Space", "fullwidth space"},
		{"Tom & Jerry", "tom and jerry"},
	}

	for _, tt := range tests {
		if got := Normalise(tt.in); got != tt.want {
			t.Errorf("Normalise(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}

	// Japanese passes through unharmed; there is nothing to fold.
	if got := Normalise("進撃の巨人"); got != "進撃の巨人" {
		t.Errorf("native title altered: %q", got)
	}
}

func TestNormaliseIsIdempotent(t *testing.T) {
	for _, s := range []string{
		"Sōsō no Frieren", "Show 2nd Season", "Fate/Zero", "K-On!!", "進撃の巨人",
	} {
		once := Normalise(s)
		if twice := Normalise(once); twice != once {
			t.Errorf("%q: %q then %q", s, once, twice)
		}
	}
}

// Normalise is called from many goroutines at once (every search and index
// build), so a shared transform.Chain would panic with a slice-bounds error.
func TestNormaliseIsConcurrencySafe(t *testing.T) {
	titles := []string{
		"Sōsō no Frieren", "Kimetsu no Yaiba", "Jūjutsu Kaisen",
		"Fullmetal Alchemist: Brotherhood", "Re:Zero — Hajimeru Isekai Seikatsu",
		"Bocchi the Rock!", "Shingeki no Kyojin", "Steins;Gate",
	}
	want := make([]string, len(titles))
	for i, s := range titles {
		want[i] = Normalise(s)
	}

	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 200 {
				for i, s := range titles {
					if got := Normalise(s); got != want[i] {
						t.Errorf("Normalise(%q) = %q under load, want %q", s, got, want[i])
						return
					}
				}
			}
		}()
	}
	wg.Wait()
}
