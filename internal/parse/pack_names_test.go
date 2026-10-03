package parse

import "testing"

// Real pack file names: audio layouts and title numbers aren't the episode.
func TestPackFileNames(t *testing.T) {
	for name, want := range map[string]int{
		"Monster.2004.S01E01.Herr.Dr.Tenma.1080p.BluRay.DUAL.FLAC.2.0.x264-Kitsune.mkv":  1,
		"Monster.2004.S01E11.511.Kinderheim.1080p.BluRay.DUAL.FLAC.2.0.x264-Kitsune.mkv": 11,
		"[CBM]_Monster_-_11_-_511_Kinderheim_[6C70C4E4].mkv":                             11,
		"[CBM]_Monster_-_02_-_Downfall_[78722558].mkv":                                   2,
	} {
		if got := Parse(name).Episode; got != want {
			t.Errorf("Parse(%q).Episode = %d, want %d", name, got, want)
		}
	}
}
