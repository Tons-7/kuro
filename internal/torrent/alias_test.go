package torrent

import "testing"

// A pack counting the season through its cours names episode 5 of the fourth
// one "45"; the picker takes any number the caller says names the episode.
func TestPickEpisodeAcceptsAnAliasNumber(t *testing.T) {
	files := []File{
		{Name: "Bleach - Sennen Kessen-hen - 44.mkv", Length: 700 << 20},
		{Name: "Bleach - Sennen Kessen-hen - 45.mkv", Length: 700 << 20},
		{Name: "Bleach - Sennen Kessen-hen - 46.mkv", Length: 700 << 20},
	}

	got, idx, ok := PickEpisode(files, 5, 45, 411)
	if !ok || idx != 1 || got.Name != files[1].Name {
		t.Errorf("picked %q (%d, %v), want the file numbered 45", got.Name, idx, ok)
	}
	if _, _, ok := PickEpisode(files, 5); ok {
		t.Error("episode 5 should not be found without its alias")
	}
	// A zero alias is no number at all.
	if _, _, ok := PickEpisode(files, 5, 0); ok {
		t.Error("a zero alias matched something")
	}
}
