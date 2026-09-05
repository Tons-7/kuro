package torrent

import "testing"

func TestEpisodeInNameIgnoresYearsAndCodecs(t *testing.T) {
	cases := map[string]int{
		"[SubsPlease] Show - 07 (1080p) [ABCD1234].mkv":                     7,
		"Detective.Conan.The.Bride.of.Halloween.2022.1080p.BluRay.x264.mkv": 0,
		"[Group] Show S02E05 [1080p].mkv":                                   5,
		"Show (2019) - 12 [BD 1080p x265 10bit].mkv":                        12,
	}

	for name, want := range cases {
		if got := episodeInName(baseName(name)); got != want {
			t.Errorf("%s\n  got %d, want %d", name, got, want)
		}
	}
}
