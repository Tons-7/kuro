package score

import (
	"testing"

	"kuro/internal/parse"
)

func TestAudioPreferenceFiltersDubOnlyReleases(t *testing.T) {
	cases := []struct {
		name   string
		want   string
		reject bool
	}{
		{"[Group] Show - 01 [1080p][English Dub].mkv", "sub", true},
		{"[Group] Show - 01 [1080p][Dual-Audio].mkv", "sub", false},
		{"[SubsPlease] Show - 01 (1080p) [ABCD1234].mkv", "sub", false},
		{"[Group] Show - 01 [1080p][Dub][Multi-Subs].mkv", "sub", false},

		{"[SubsPlease] Show - 01 (1080p).mkv", "dub", true},
		{"[Group] Show - 01 [English Dub].mkv", "dub", false},
		{"[Group] Show - 01 [Dual Audio].mkv", "dub", false},

		{"[Group] Show - 01 [1080p][English Dub].mkv", "either", false},
	}

	for _, c := range cases {
		rel := parse.Parse(c.name)
		if got := audioMismatch(rel, c.want); got != c.reject {
			t.Errorf("%s want=%s\n  rejected %v, expected %v", c.name, c.want, got, c.reject)
		}
	}
}

func TestDubOnlyIsDisqualifiedForASubWatcher(t *testing.T) {
	prefs := DefaultPreferences()

	dub := candidate("[Group] Show - 01 [1080p][English Dub].mkv", 500, 1<<30)
	if got := Rank([]Candidate{dub}, prefs)[0]; got.AutoPick {
		t.Fatal("a dub-only release was auto-picked for a sub watcher")
	}

	sub := candidate("[SubsPlease] Show - 01 (1080p) [ABCD1234].mkv", 20, 1<<30)
	if got := Rank([]Candidate{sub}, prefs)[0]; !got.AutoPick {
		t.Fatalf("a normal subbed release was blocked: %q", got.Blocked)
	}
}
