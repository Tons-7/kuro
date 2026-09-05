package score

import "testing"

func rejectionRules(c Candidate, prefs Preferences) []string {
	out := []string{}
	for _, r := range evaluateSpecs(c, prefs) {
		out = append(out, r.Rule)
	}
	return out
}

// A Blu-ray disc image is hours of menus and VOB structure, not an episode.
func TestDiscImagesAreRejected(t *testing.T) {
	prefs := DefaultPreferences()

	for _, title := range []string{
		"[BDMV] Lupin III vs Detective Conan 2009 1080p USA Blu-ray AVC DTS-HD MA",
		"Some Show Complete Series DVDISO",
		"Show S01 BDISO",
	} {
		got := Rank([]Candidate{candidate(title, 40, 8<<30)}, prefs)[0]
		if got.AutoPick {
			t.Errorf("accepted a disc image: %s", title)
		}
	}

	// A normal Blu-ray encode is not a disc image.
	ok := candidate("[Judas] Show - 01 [BD 1080p HEVC 10bit].mkv", 40, 1<<30)
	if got := Rank([]Candidate{ok}, prefs)[0]; !got.AutoPick {
		t.Errorf("a BD encode was rejected: %q", got.Blocked)
	}
}

func TestSamplesAreRejected(t *testing.T) {
	got := Rank([]Candidate{
		candidate("[Group] Show - 01 sample [1080p].mkv", 30, 20<<20),
	}, DefaultPreferences())[0]

	if got.AutoPick {
		t.Fatal("a sample clip was offered as the episode")
	}
}

// The first refusing tier is what gets reported; a dead swarm should not be
// explained as a size problem.
func TestRejectionsStopAtTheFirstTier(t *testing.T) {
	prefs := DefaultPreferences()
	prefs.MaxAutoBytes = 1 << 20

	dead := candidate("[Group] Show - 01 [1080p].mkv", 0, 8<<30)
	rules := rejectionRules(dead, prefs)

	if len(rules) != 1 || rules[0] != "seeders" {
		t.Fatalf("rules = %v, want only the seeders rule", rules)
	}
}

func TestRejectionCarriesItsRule(t *testing.T) {
	prefs := DefaultPreferences()
	prefs.AllowHi10P = false

	got := Rank([]Candidate{
		candidate("[Group] Show - 01 [1080p Hi10P AAC].mkv", 30, 1<<30),
	}, prefs)[0]

	if len(got.Rejections) == 0 {
		t.Fatal("no rejection recorded")
	}
	if got.Rejections[0].Rule != "hi10p" {
		t.Fatalf("rule = %q", got.Rejections[0].Rule)
	}
	if got.Blocked != got.Rejections[0].Reason {
		t.Fatalf("Blocked = %q, rejection = %q", got.Blocked, got.Rejections[0].Reason)
	}
}

func TestAcceptedReleaseHasNoRejections(t *testing.T) {
	got := Rank([]Candidate{
		candidate("[SubsPlease] Show - 01 (1080p) [ABCD1234].mkv", 120, 1<<30),
	}, DefaultPreferences())[0]

	if !got.AutoPick || len(got.Rejections) != 0 {
		t.Fatalf("blocked %q, rejections %v", got.Blocked, got.Rejections)
	}
}
