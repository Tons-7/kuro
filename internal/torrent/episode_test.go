package torrent

import "testing"

func seasonPack() []File {
	files := []File{
		{Name: "Show/Extras/sample.mkv", Length: 20 << 20},
		{Name: "Show/cover.jpg", Length: 200 << 10},
		{Name: "Show/Show - 01 [1080p].mkv", Length: 1400 << 20},
		{Name: "Show/Show - 02 [1080p].mkv", Length: 1350 << 20},
		{Name: "Show/Show - 10 [1080p].mkv", Length: 1380 << 20},
		{Name: "Show/Show - 11 [1080p].mkv", Length: 2900 << 20},
		{Name: "Show/subs/Show - 10.ass", Length: 60 << 10},
	}
	return files
}

// Taking the largest file returns whichever episode happens to be biggest, so
// the number in the filename has to decide.
func TestPickEpisodeUsesTheNumberNotTheSize(t *testing.T) {
	got, idx, ok := PickEpisode(seasonPack(), 10, 0)
	if !ok {
		t.Fatal("episode 10 not found")
	}
	if got.Name != "Show/Show - 10 [1080p].mkv" {
		t.Fatalf("picked %q at index %d", got.Name, idx)
	}

	// Episode 11 is the largest file; asking for 1 must not return it.
	first, _, _ := PickEpisode(seasonPack(), 1, 0)
	if first.Name != "Show/Show - 01 [1080p].mkv" {
		t.Fatalf("picked %q for episode 1", first.Name)
	}
}

func TestPickEpisodeIgnoresSamplesAndSubtitles(t *testing.T) {
	if _, _, ok := PickEpisode(seasonPack(), 99, 0); ok {
		t.Fatal("a missing episode should report not found")
	}

	// The sample is an mkv containing no episode number and is far below the
	// median size.
	got, _, ok := PickEpisode(seasonPack(), 2, 0)
	if !ok || got.Name != "Show/Show - 02 [1080p].mkv" {
		t.Fatalf("picked %q", got.Name)
	}
}

func TestPickEpisodeSingleFileTorrent(t *testing.T) {
	files := []File{{Name: "[Group] Show - 07 [1080p].mkv", Length: 1400 << 20}}

	for _, ep := range []int{7, 0} {
		got, _, ok := PickEpisode(files, ep, 0)
		if !ok || got.Name != files[0].Name {
			t.Fatalf("episode %d picked %q", ep, got.Name)
		}
	}

	// A cour released as 01-12 but catalogued as 17-28.
	cour := []File{{Name: "[Group] Show - 02 [1080p].mkv", Length: 1400 << 20}}
	if _, _, ok := PickEpisode(cour, 18, 2); !ok {
		t.Fatal("the alternate numbering was not accepted")
	}
}

// A lone file used to be taken on trust, so a feature film played as episode
// 1202 of a long-running series.
func TestPickEpisodeRejectsALoneFileThatIsNotTheEpisode(t *testing.T) {
	film := []File{{
		Name:   "Detective.Conan.The.Bride.of.Halloween.2022.1080p.BluRay.x264.mkv",
		Length: 6 << 30,
	}}
	if _, _, ok := PickEpisode(film, 1202, 0); ok {
		t.Fatal("a film with no episode number was accepted as episode 1202")
	}

	wrong := []File{{Name: "[Group] Show - 03 [1080p].mkv", Length: 1400 << 20}}
	if _, _, ok := PickEpisode(wrong, 1202, 0); ok {
		t.Fatal("episode 3 was accepted as episode 1202")
	}

	// A one-episode entry is the case an unnumbered file legitimately covers.
	if _, _, ok := PickEpisode(film, 1, 0); !ok {
		t.Fatal("a film should still play as the single episode of its own entry")
	}
}

func TestPickEpisodeNoVideos(t *testing.T) {
	if _, _, ok := PickEpisode([]File{{Name: "readme.txt", Length: 100}}, 1, 0); ok {
		t.Fatal("matched a non-video file")
	}
	if _, _, ok := PickEpisode(nil, 1, 0); ok {
		t.Fatal("matched something in an empty list")
	}
}

// Resolution, codec, year and CRC are all numbers in a filename.
func TestEpisodeInName(t *testing.T) {
	tests := map[string]int{
		"[SubsPlease] Sousou no Frieren - 10 (1080p) [7D35515E]": 10,
		"Show - S01E07 1080p WEB-DL x264":                        7,
		"[Group] Show - 1100 (1080p)":                            1100,
		"Show.S01E04.1080p.CR.WEB-DL.AAC2.0.H.264":               4,
		"[Group] Show - 05v2 (1080p)":                            5,
		"[Group] Show 2nd Season - 03 [1080p][HEVC]":             3,
		"sample":                            0,
		"cover":                             0,
		"[Group] Show [1080p][x265][10bit]": 0,
	}

	for name, want := range tests {
		if got := episodeInName(name); got != want {
			t.Errorf("episodeInName(%q) = %d, want %d", name, got, want)
		}
	}
}

func TestBaseName(t *testing.T) {
	tests := map[string]string{
		"Show/Show - 01 [1080p].mkv": "Show - 01 [1080p]",
		`Show\Show - 01.mkv`:         "Show - 01",
		"Show - 01.mkv":              "Show - 01",
		"noextension":                "noextension",
	}
	for in, want := range tests {
		if got := baseName(in); got != want {
			t.Errorf("baseName(%q) = %q, want %q", in, got, want)
		}
	}
}
