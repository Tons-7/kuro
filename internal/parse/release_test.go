package parse

import "testing"

func TestParseSingleEpisodes(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want Release
	}{
		{
			name: "simulcast remux",
			in:   "[SubsPlease] Sousou no Frieren - 10 (1080p) [7D35515E].mkv",
			want: Release{
				Title: "Sousou no Frieren", Group: "SubsPlease", Episode: 10,
				Resolution: "1080p", CRC32: "7D35515E", Extension: "mkv",
			},
		},
		{
			name: "season in title",
			in:   "[SubsPlease] Sousou no Frieren S2 - 07 (1080p) [ABCD1234].mkv",
			want: Release{
				Title: "Sousou no Frieren", Group: "SubsPlease", Season: 2, Episode: 7,
				Resolution: "1080p", CRC32: "ABCD1234", Extension: "mkv",
			},
		},
		{
			name: "source and codec inline",
			in:   "[Erai-raws] Some Show - 07 [1080p HIDIVE WEB-DL AVC AAC][ABCD1234]",
			want: Release{
				Title: "Some Show", Group: "Erai-raws", Episode: 7,
				Resolution: "1080p", Source: "WEB", Codec: "H264",
				Audio: []string{"AAC"}, CRC32: "ABCD1234",
			},
		},
		{
			name: "scene style with trailing group",
			in:   "Some Show - S01E04 1080p CR WEB-DL DUAL AAC2.0 H.264-VARYG",
			want: Release{
				Title: "Some Show", Group: "VARYG", Season: 1, Episode: 4,
				Resolution: "1080p", Source: "WEB", Codec: "H264",
				Audio: []string{"AAC"}, DualAudio: true,
			},
		},
		{
			name: "blu-ray remux with dual audio",
			in:   "Some Show - S01E01 (BD Remux 1080p AVC FLAC AAC) [Dual Audio] [PMR].mkv",
			want: Release{
				Title: "Some Show", Group: "PMR", Season: 1, Episode: 1,
				Resolution: "1080p", Source: "BDRemux", Codec: "H264",
				Audio: []string{"FLAC", "AAC"}, DualAudio: true, Extension: "mkv",
			},
		},
		{
			name: "ten bit hevc",
			in:   "[ASW] Some Show - 12 [1080p HEVC x265 10Bit][AAC]",
			want: Release{
				Title: "Some Show", Group: "ASW", Episode: 12,
				Resolution: "1080p", Codec: "HEVC", BitDepth: 10, Audio: []string{"AAC"},
			},
		},
		{
			name: "version suffix",
			in:   "[Group] Some Show - 05v2 (1080p) [DEADBEEF].mkv",
			want: Release{
				Title: "Some Show", Group: "Group", Episode: 5, Version: 2,
				Resolution: "1080p", CRC32: "DEADBEEF", Extension: "mkv",
			},
		},
		{
			name: "raw transport stream",
			in:   "[shincaps] Some Show 07 (BS11 1920x1080 MPEG2 AAC).ts",
			want: Release{
				Title: "Some Show 07", Group: "shincaps",
				Resolution: "1080p", Source: "TV", Codec: "MPEG2",
				Audio: []string{"AAC"}, Extension: "ts",
			},
		},
		{
			name: "three digit episode",
			in:   "[Group] Long Runner - 1100 (1080p) [ABCD1234].mkv",
			want: Release{
				Title: "Long Runner", Group: "Group", Episode: 1100,
				Resolution: "1080p", CRC32: "ABCD1234", Extension: "mkv",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Parse(tt.in)
			compare(t, got, tt.want)
		})
	}
}

func TestParseBatches(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want Release
	}{
		{
			name: "explicit batch tag",
			in:   "[SubsPlease] Sousou no Frieren (01-28) [1080p] [Batch]",
			want: Release{
				Title: "Sousou no Frieren", Group: "SubsPlease",
				Episode: 1, EpisodeEnd: 28, Resolution: "1080p", Batch: true,
			},
		},
		{
			name: "season batch",
			in:   "[SubsPlease] Sousou no Frieren S2 (01-10) [1080p] [Batch]",
			want: Release{
				Title: "Sousou no Frieren", Group: "SubsPlease", Season: 2,
				Episode: 1, EpisodeEnd: 10, Resolution: "1080p", Batch: true,
			},
		},
		{
			name: "complete series",
			in:   "[Group] Some Show (Complete) (1080p) (Dual Audio)",
			want: Release{
				Title: "Some Show", Group: "Group",
				Resolution: "1080p", DualAudio: true, Batch: true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			compare(t, Parse(tt.in), tt.want)
		})
	}
}

// A resolution written as WxH must not be mistaken for a checksum once the
// brackets are stripped.
func TestResolutionIsNotReadAsChecksum(t *testing.T) {
	got := Parse("[Group] Some Show - 07 (1920x1080) [AABBCCDD].mkv")
	if got.CRC32 != "AABBCCDD" {
		t.Fatalf("crc32 = %q", got.CRC32)
	}
	if got.Resolution != "1080p" {
		t.Fatalf("resolution = %q", got.Resolution)
	}
}

// Longest-first matching keeps "BD Remux" from being read as plain "BD".
func TestRemuxOutranksBluRay(t *testing.T) {
	if got := Parse("Show (BD Remux 1080p)").Source; got != "BDRemux" {
		t.Fatalf("source = %q, want BDRemux", got)
	}
	if got := Parse("Show (BDRip 1080p)").Source; got != "BD" {
		t.Fatalf("source = %q, want BD", got)
	}
}

// Hi10P names H.264 High 10 Profile, so it settles both codec and bit depth
// even when the release never writes "x264".
func TestHi10PImpliesH264(t *testing.T) {
	got := Parse("[Group] Some Show - 07 [1080p Hi10P AAC].mkv")
	if got.Codec != "H264" {
		t.Errorf("codec = %q, want H264", got.Codec)
	}
	if got.BitDepth != 10 {
		t.Errorf("bit depth = %d, want 10", got.BitDepth)
	}

	// An explicit codec still wins over the inference.
	if got := Parse("[Group] Show - 07 [1080p HEVC 10bit]"); got.Codec != "HEVC" {
		t.Errorf("codec = %q, want the explicit HEVC", got.Codec)
	}
}

func TestOrdinalSeason(t *testing.T) {
	if got := Parse("[DB] Some Show 2nd Season [BD1080p][HEVC-x265]").Season; got != 2 {
		t.Fatalf("season = %d, want 2", got)
	}
}

func TestSpecialsPackCarriesItsSeason(t *testing.T) {
	got := Parse("[Judas] Some Show S02SP01-02 [1080p][HEVC x265 10bit][Multi-Subs] (Weekly)")
	if got.Season != 2 || got.Episode != 0 || !got.Batch {
		t.Fatalf("season=%d episode=%d batch=%v, want a season 2 pack", got.Season, got.Episode, got.Batch)
	}
	if got.Title != "Some Show" {
		t.Errorf("title = %q", got.Title)
	}
}

// "E06" is episode 6; read as unnumbered it would pass for any episode.
func TestMarkedEpisodeForms(t *testing.T) {
	tests := []struct {
		in      string
		episode int
		version int
	}{
		{"[Onalrie] Some Show - E06 [1080p WEBRip AV1].mkv", 6, 0},
		{"[Group] Some Show - EP06 [1080p].mkv", 6, 0},
		{"[Group] Some Show - Ep. 6 [1080p].mkv", 6, 0},
		{"[Group] Some Show - Episode 6 [1080p].mkv", 6, 0},
		{"[Group] Some Show E06v2 [1080p].mkv", 6, 2},
		// A checksum and a codec both start with the same letter.
		{"[Group] Some Show - 05 [1080p][E1234567].mkv", 5, 0},
		{"[Group] Some Show - 05 [1080p x265 10bit E-AC-3 2.0].mkv", 5, 0},
		{"[Group] Some Show - 05 [1080p EAC3].mkv", 5, 0},
	}
	for _, tt := range tests {
		got := Parse(tt.in)
		if got.Episode != tt.episode || got.Version != tt.version {
			t.Errorf("episode=%d version=%d, want %d/%d: %s",
				got.Episode, got.Version, tt.episode, tt.version, tt.in)
		}
		if got.Title != "Some Show" {
			t.Errorf("title = %q: %s", got.Title, tt.in)
		}
	}
}

// Episodes listed rather than ranged still say which ones are in the pack.
func TestListedEpisodesAreAPack(t *testing.T) {
	got := Parse("Some Show [E41 E42 E43] [1080p DCPrip][AV1][OPUS]")
	if got.Episode != 41 || got.EpisodeEnd != 43 || !got.Batch {
		t.Fatalf("episode=%d end=%d batch=%v, want 41-43", got.Episode, got.EpisodeEnd, got.Batch)
	}
	if got.Title != "Some Show" {
		t.Errorf("title = %q", got.Title)
	}
}

// A span is a pack, tagged or not, even from zero; only the spaced form is a title's number.
func TestUntaggedRangeIsABatch(t *testing.T) {
	for _, in := range []string{
		"[Group] Some Show (01-12) [1080p]",
		"[Group] Some Show (00-12) [1080p]",
		"Some Show 00-12 [1080p]",
	} {
		if got := Parse(in); !got.Batch || got.EpisodeEnd != 12 {
			t.Errorf("episode=%d end=%d batch=%v: %s", got.Episode, got.EpisodeEnd, got.Batch, in)
		}
	}
	if got := Parse("[Group] Steins;Gate 0 - 03 [1080p].mkv"); got.Episode != 3 || got.Batch {
		t.Errorf("episode=%d batch=%v, want episode 3", got.Episode, got.Batch)
	}
}

func TestParseNeverPanics(t *testing.T) {
	for _, in := range []string{
		"", " ", "[]", "()", "[][][]", "----", "[Group]", "- 07",
		"[Group] - - 07 - (1080p) [", "S01E", "(01-)", "999999999999999999999",
		"[G] Show - 01 [10bit]", "[G] Show [Hi10P]", "[G] Show [8 bit]",
		"Show 2nd Season", "[G] Show (01-)", "[G] Show - 01v [x265]",
	} {
		Parse(in)
	}
}

func compare(t *testing.T, got, want Release) {
	t.Helper()

	if got.Title != want.Title {
		t.Errorf("Title = %q, want %q", got.Title, want.Title)
	}
	if got.Group != want.Group {
		t.Errorf("Group = %q, want %q", got.Group, want.Group)
	}
	if got.Season != want.Season {
		t.Errorf("Season = %d, want %d", got.Season, want.Season)
	}
	if got.Episode != want.Episode {
		t.Errorf("Episode = %d, want %d", got.Episode, want.Episode)
	}
	if got.EpisodeEnd != want.EpisodeEnd {
		t.Errorf("EpisodeEnd = %d, want %d", got.EpisodeEnd, want.EpisodeEnd)
	}
	if got.Version != want.Version {
		t.Errorf("Version = %d, want %d", got.Version, want.Version)
	}
	if got.Resolution != want.Resolution {
		t.Errorf("Resolution = %q, want %q", got.Resolution, want.Resolution)
	}
	if got.Source != want.Source {
		t.Errorf("Source = %q, want %q", got.Source, want.Source)
	}
	if got.Codec != want.Codec {
		t.Errorf("Codec = %q, want %q", got.Codec, want.Codec)
	}
	if got.BitDepth != want.BitDepth {
		t.Errorf("BitDepth = %d, want %d", got.BitDepth, want.BitDepth)
	}
	if got.DualAudio != want.DualAudio {
		t.Errorf("DualAudio = %v, want %v", got.DualAudio, want.DualAudio)
	}
	if got.Batch != want.Batch {
		t.Errorf("Batch = %v, want %v", got.Batch, want.Batch)
	}
	if got.CRC32 != want.CRC32 {
		t.Errorf("CRC32 = %q, want %q", got.CRC32, want.CRC32)
	}
	if got.Extension != want.Extension {
		t.Errorf("Extension = %q, want %q", got.Extension, want.Extension)
	}
	if len(want.Audio) > 0 {
		if len(got.Audio) != len(want.Audio) {
			t.Errorf("Audio = %v, want %v", got.Audio, want.Audio)
		} else {
			for i := range want.Audio {
				if got.Audio[i] != want.Audio[i] {
					t.Errorf("Audio = %v, want %v", got.Audio, want.Audio)
					break
				}
			}
		}
	}
}
