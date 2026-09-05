package library

import "testing"

func TestShortTermsPrefersDistinctiveWords(t *testing.T) {
	got := shortTerms([]string{"Onaji Zemi no Someya-san ga Sexy Joyuu Datta Hanashi."}, "")
	if len(got) == 0 {
		t.Fatal("no fallback queries produced")
	}
	for _, q := range got {
		if len(q) < 4 {
			t.Errorf("query %q is too short to narrow anything", q)
		}
	}
}

func TestSignificantWordsDropsParticlesAndNumbers(t *testing.T) {
	got := significantWords("The Movie of a Season 2 Part 3: Hanashi")
	for _, w := range got {
		switch w {
		case "The", "Movie", "Season", "Part", "2", "3":
			t.Errorf("%q should not count as distinctive", w)
		}
	}
}
