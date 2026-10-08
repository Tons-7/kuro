package film

import "testing"

// The Garden of Sinners as the catalogue has it: one entry per film.
var series = map[string]Entry{
	"ch1":    {[]string{"Kara no Kyoukai: Fukan Fuukei", "the Garden of sinners Chapter 1: Thanatos. (Overlooking View)"}},
	"ch2":    {[]string{"Kara no Kyoukai: Satsujin Kousatsu (Zen)", "the Garden of sinners Chapter 2: …and nothing heart. (Murder Speculation Part A)"}},
	"ch3":    {[]string{"Kara no Kyoukai: Tsuukaku Zanryuu", "the Garden of sinners Chapter 3: ever cry, never life. (Remaining Sense of Pain)"}},
	"ch4":    {[]string{"Kara no Kyoukai: Garan no Dou", "the Garden of sinners Chapter 4: garan-no-dou. (The Hollow Shrine)"}},
	"ch5":    {[]string{"Kara no Kyoukai: Mujun Rasen", "the Garden of sinners Chapter 5: Paradox Paradigm"}},
	"ch6":    {[]string{"Kara no Kyoukai: Boukyaku Rokuon", "the Garden of sinners Chapter 6: Fairy Tale. (Oblivion Recording)"}},
	"ch7":    {[]string{"Kara no Kyoukai: Satsujin Kousatsu (Kou)", "the Garden of sinners Chapter 7: ……not nothing heart. (Murder Speculation Part B)"}},
	"remix":  {[]string{"Kara no Kyoukai Remix: Gate of seventh heaven"}},
	"ch8":    {[]string{"Kara no Kyoukai: Shuushou", "the Garden of sinners Chapter 8: The Final Chapter"}},
	"future": {[]string{"Kara no Kyoukai: Mirai Fukuin", "the Garden of sinners -recalled out summer-"}},
	"chorus": {[]string{"Kara no Kyoukai: Mirai Fukuin - extra chorus"}},
}

func keyFor(name string) Key {
	var rest []Entry
	for n, e := range series {
		if n != name {
			rest = append(rest, e)
		}
	}
	return New(series[name], rest)
}

const gb = 1 << 30

func files(pairs ...any) []File {
	var out []File
	for i := 0; i < len(pairs); i += 2 {
		out = append(out, File{Name: pairs[i+1].(string), Length: int64(pairs[i].(float64) * gb)})
	}
	return out
}

// File lists read from the swarm on 2026-10-08, sizes in GB.
var batches = map[string][]File{
	"AnimeTime": files(
		0.15, "09.5 Kara No Kyoukai - Mirai Fukuin - Extra Chorus-11.mkv",
		0.45, "01 Kara No Kyoukai - Fukan Fuukei-1.mkv",
		0.46, "04 Kara No Kyoukai - Garan No Dou-4.mkv",
		0.55, "02 Kara No Kyoukai - Satsujin Kousatsu (Zen)-2.mkv",
		0.60, "06 Kara No Kyoukai - Boukyaku Rokuon-6.mkv",
		0.67, "03 Kara No Kyoukai - Tsuukaku Zanryuu-3.mkv",
		0.69, "09 Kara No Kyoukai - Mirai Fukuin-10.mkv",
		0.98, "06.5 Kara No Kyoukai Remix - Gate Of Seventh Heaven-7.mkv",
		1.10, "07 Kara No Kyoukai - Satsujin Kousatsu (Kou)-8.mkv",
		1.11, "08 Kara No Kyoukai - Shuushou-9.mkv",
		1.73, "05 Kara No Kyoukai - Mujun Rasen-5.mkv",
	),
	"ANE": files(
		0.09, "Cinema Intros/[ANE] Kara no Kyoukai - Cinema Intro 1 [BDRip 1080p x264 FLAC].mkv",
		0.09, "Cinema Intros/[ANE] Kara no Kyoukai - Cinema Intro 2 [BDRip 1080p x264 FLAC].mkv",
		0.12, "Cinema Intros/[ANE] Kara no Kyoukai - Cinema Intro 3 [BDRip 1080p x264 FLAC].mkv",
		0.12, "Cinema Intros/[ANE] Kara no Kyoukai - Cinema Intro 4v0 [BDRip 1080p x264 FLAC].mkv",
		0.10, "Cinema Intros/[ANE] Kara no Kyoukai - Cinema Intro 5v0 [BDRip 1080p x264 FLAC].mkv",
		0.10, "Cinema Intros/[ANE] Kara no Kyoukai - Cinema Intro 6 [BDRip 1080p x264 FLAC].mkv",
		0.12, "Cinema Intros/[ANE] Kara no Kyoukai - Cinema Intro 7v0 [BDRip 1080p x264 FLAC].mkv",
		4.65, "[ANE] Kara no Kyoukai - 1 - Overlooking View [BDRip 1080p x264 FLAC].mkv",
		5.62, "[ANE] Kara no Kyoukai - 2 - Murder Speculation (Part A) [BDRip 1080p x264 FLAC].mkv",
		6.32, "[ANE] Kara no Kyoukai - 3 - Remaining Sense of Pain [BDRip 1080p x264 FLAC].mkv",
		4.46, "[ANE] Kara no Kyoukai - 4v0 - The Hollow Shrine [BDRip 1080p x264 FLAC].mkv",
		20.83, "[ANE] Kara no Kyoukai - 5v0 - Paradox Paradigm [BDRip 1080p x264 FLAC].mkv",
		5.98, "[ANE] Kara no Kyoukai - 6 - Oblivion Recording [BDRip 1080p x264 FLAC].mkv",
		17.40, "[ANE] Kara no Kyoukai - 7v0 - Murder Speculation (Part B) [BDRip 1080p x264 FLAC].mkv",
		4.85, "[ANE] Kara no Kyoukai - Epilogue [BDRip 1080p x264 FLAC].mkv",
		8.82, "[ANE] Kara no Kyoukai - Remix v0 - Gate of Seventh Heaven [BDRip 1080p x264 FLAC].mkv",
	),
	"Fortissimo": files(
		11.31, "[Fortissimo] Kara no Kyoukai - 5.0 - Paradox Spiral.mkv",
		1.24, "[Fortissimo] Kara no Kyoukai - 2.0 - Murder Speculation (Part 1).mkv",
		1.55, "[Fortissimo] Kara no Kyoukai - 3.0 - Remaining Sense of Pain.mkv",
		1.18, "[Fortissimo] Kara no Kyoukai - 4.0 - The Hollow Shrine.mkv",
		1.06, "[Fortissimo] Kara no Kyoukai - 1.0 - Overlooking View.mkv",
		1.50, "[Fortissimo] Kara no Kyoukai - 6.0 - Oblivion Recording.mkv",
		3.06, "[Fortissimo] Kara no Kyoukai - 6.5 - Remix - Gate of Seventh Heaven (recap).mkv",
		4.08, "[Fortissimo] Kara no Kyoukai - 7.0 - Murder Speculation (Part 2).mkv",
		2.19, "[Fortissimo] Kara no Kyoukai - 8.0 - Epilogue.mkv",
		3.20, "[Fortissimo] Kara no Kyoukai - 9.0 - Future Gospel.mkv",
		1.06, "[Fortissimo] Kara no Kyoukai - 9.5 - Future Gospel - Extra Chorus.mkv",
	),
	"Trix": files(
		0.25, "[Trix] Kara No Kyoukai 1 - Fukan Fuukei (2007) (BD 1080p AV1).mkv",
		0.13, "[Trix] Kara No Kyoukai 10 - Mirai Fukuin - Extra Chorus (2013) (BD 1080p AV1).mkv",
		0.31, "[Trix] Kara No Kyoukai 2 - Satsujin Kousatsu (Zen) (2007) (BD 1080p AV1).mkv",
		0.36, "[Trix] Kara No Kyoukai 3 - Tsuukaku Zanryuu (2008) (BD 1080p AV1).mkv",
		0.25, "[Trix] Kara No Kyoukai 4 - Garan No Dou (2008) (BD 1080p AV1).mkv",
		0.74, "[Trix] Kara No Kyoukai 5 - Mujun Rasen (2008) (BD 1080p AV1).mkv",
		0.32, "[Trix] Kara No Kyoukai 6 - Boukyaku Rokuon (2008) (BD 1080p AV1).mkv",
		0.55, "[Trix] Kara No Kyoukai 7 - Satsujin Kousatsu (Go) (2009) (BD 1080p AV1).mkv",
		0.61, "[Trix] Kara No Kyoukai 8 - Shuushou (2011) (BD 1080p AV1).mkv",
		0.47, "[Trix] Kara No Kyoukai 9 - Mirai Fukuin (2013) (BD 1080p AV1).mkv",
	),
	// Mislabelled as uploaded: two files carry chapter 6's subtitle.
	"Judas": files(
		4.17, "[Judas] Kara no Kyoukai - 05 - Mujun Rasen.mkv",
		0.57, "[Judas] Kara no Kyoukai - 02 - Satsujin Kousatsu (Zen).mkv",
		0.75, "[Judas] Kara no Kyoukai - 03 - Tsuukaku Zanryuu.mkv",
		0.54, "[Judas] Kara no Kyoukai - 04 - Garan No Dou.mkv",
		0.47, "[Judas] Kara no Kyoukai - 01 - Fukan Fuukei.mkv",
		0.65, "[Judas] Kara no Kyoukai - 06 - Boukyaku Rokuon.mkv",
		1.24, "[Judas] Kara no Kyoukai - 07 - Boukyaku Rokuon.mkv",
		1.26, "Extras/[Judas] Kara no Kyoukai - 06.5 - Gate of seventh heaven.mkv",
		1.21, "Extras/[Judas] Kara no Kyoukai - 08 - Satsujin Kousatsu (Go).mkv",
		0.86, "Extras/[Judas] Kara no Kyoukai - 09 - Mirai Fukuin.mkv",
		0.26, "Extras/[Judas] Kara no Kyoukai - 09.5 - Mirai Fukuin - Extra Chorus.mkv",
	),
}

func TestPickFindsEachFilmInRealBatches(t *testing.T) {
	// "" is a film the batch cannot be trusted for: absent, unnamed, or mislabelled.
	want := map[string]map[string]string{
		"AnimeTime": {
			"ch1": "01 Kara No Kyoukai - Fukan Fuukei-1.mkv", "ch2": "02 Kara No Kyoukai - Satsujin Kousatsu (Zen)-2.mkv",
			"ch3": "03 Kara No Kyoukai - Tsuukaku Zanryuu-3.mkv", "ch4": "04 Kara No Kyoukai - Garan No Dou-4.mkv",
			"ch5": "05 Kara No Kyoukai - Mujun Rasen-5.mkv", "ch6": "06 Kara No Kyoukai - Boukyaku Rokuon-6.mkv",
			"ch7": "07 Kara No Kyoukai - Satsujin Kousatsu (Kou)-8.mkv", "ch8": "08 Kara No Kyoukai - Shuushou-9.mkv",
			"remix": "06.5 Kara No Kyoukai Remix - Gate Of Seventh Heaven-7.mkv", "future": "09 Kara No Kyoukai - Mirai Fukuin-10.mkv",
			"chorus": "09.5 Kara No Kyoukai - Mirai Fukuin - Extra Chorus-11.mkv",
		},
		"ANE": {
			"ch1": "[ANE] Kara no Kyoukai - 1 - Overlooking View [BDRip 1080p x264 FLAC].mkv",
			"ch2": "[ANE] Kara no Kyoukai - 2 - Murder Speculation (Part A) [BDRip 1080p x264 FLAC].mkv",
			"ch3": "[ANE] Kara no Kyoukai - 3 - Remaining Sense of Pain [BDRip 1080p x264 FLAC].mkv",
			"ch4": "[ANE] Kara no Kyoukai - 4v0 - The Hollow Shrine [BDRip 1080p x264 FLAC].mkv",
			"ch5": "[ANE] Kara no Kyoukai - 5v0 - Paradox Paradigm [BDRip 1080p x264 FLAC].mkv",
			// Not "Cinema Intro 6", which carries the same number.
			"ch6":   "[ANE] Kara no Kyoukai - 6 - Oblivion Recording [BDRip 1080p x264 FLAC].mkv",
			"ch7":   "[ANE] Kara no Kyoukai - 7v0 - Murder Speculation (Part B) [BDRip 1080p x264 FLAC].mkv",
			"remix": "[ANE] Kara no Kyoukai - Remix v0 - Gate of Seventh Heaven [BDRip 1080p x264 FLAC].mkv",
			// "Epilogue" is in no catalogue title, and nothing else says chapter 8.
			"ch8": "", "future": "", "chorus": "",
		},
		"Fortissimo": {
			"ch1": "[Fortissimo] Kara no Kyoukai - 1.0 - Overlooking View.mkv", "ch2": "[Fortissimo] Kara no Kyoukai - 2.0 - Murder Speculation (Part 1).mkv",
			"ch5": "[Fortissimo] Kara no Kyoukai - 5.0 - Paradox Spiral.mkv", "ch6": "[Fortissimo] Kara no Kyoukai - 6.0 - Oblivion Recording.mkv",
			"ch7": "[Fortissimo] Kara no Kyoukai - 7.0 - Murder Speculation (Part 2).mkv", "ch8": "[Fortissimo] Kara no Kyoukai - 8.0 - Epilogue.mkv",
			"remix":  "[Fortissimo] Kara no Kyoukai - 6.5 - Remix - Gate of Seventh Heaven (recap).mkv",
			"chorus": "[Fortissimo] Kara no Kyoukai - 9.5 - Future Gospel - Extra Chorus.mkv",
			// "Future Gospel" is a name the catalogue does not carry, and the entry states no chapter.
			"future": "",
		},
		"Trix": {
			"ch6": "[Trix] Kara No Kyoukai 6 - Boukyaku Rokuon (2008) (BD 1080p AV1).mkv", "ch7": "[Trix] Kara No Kyoukai 7 - Satsujin Kousatsu (Go) (2009) (BD 1080p AV1).mkv",
			"ch8": "[Trix] Kara No Kyoukai 8 - Shuushou (2011) (BD 1080p AV1).mkv", "future": "[Trix] Kara No Kyoukai 9 - Mirai Fukuin (2013) (BD 1080p AV1).mkv",
			"chorus": "[Trix] Kara No Kyoukai 10 - Mirai Fukuin - Extra Chorus (2013) (BD 1080p AV1).mkv",
		},
		"Judas": {
			"ch2": "[Judas] Kara no Kyoukai - 02 - Satsujin Kousatsu (Zen).mkv",
			// Two files claim the subtitle; the number settles it.
			"ch6": "[Judas] Kara no Kyoukai - 06 - Boukyaku Rokuon.mkv",
			// File 07 is named as chapter 6 and file 08 as a film numbered otherwise: neither is trusted.
			"ch7": "", "ch8": "",
		},
	}
	for batch, films := range want {
		for film, name := range films {
			i, ok := keyFor(film).Pick(batches[batch])
			got := ""
			if ok {
				got = batches[batch][i].Name
			}
			if got != name {
				t.Errorf("%s, %s: picked %q, want %q", batch, film, got, name)
			}
		}
	}
}

func TestMatchSortsRealReleaseNames(t *testing.T) {
	ch6 := keyFor("ch6")
	for name, want := range map[string]Verdict{
		"[JacobSwaggedUp] Kara no Kyoukai 6: Boukyaku Rokuon | The Garden of Sinners Chapter 6: Oblivion Recording (BD 1280x720) [MP4 Movie]": Ours,
		"[N LogN EG] Kara no Kyoukai (6) [H264 PS3 1080p AC3] [F43AEA3A].mp4":                                                                 Ours,
		"[gg-TakaJun]_Kara_no_Kyoukai_-_The_Garden_of_Sinners_-_06_[238F9CE7].mkv":                                                            Ours,

		"[nekotan] The Garden of Sinners S01E01 Chapter 1: Thanatos. (Overlooking View) (BD 1080p x265 10-bit Opus) | Kara no Kyoukai: Fukan Fuukei": Other,
		"[UTW]_Kara_no_Kyoukai_-_Mirai_Fukuin_[BD][h264-1080p_FLAC][76F03FE0].mkv":                                                                   Other,
		"[N LogN EG] Kara no Kyoukai (7) [H264 PS3 1080p AC3] [E65460CE].mp4":                                                                        Other,
		"[66]_Kara_no_Kyoukai_-_4_(720p,BluRay)_[8A24AC6C].mkv":                                                                                      Other,
		"[HalfOrange] Kara no Kyoukai 01v2 Overlooking View [855p-Hi444P][5.1-Opus][EAA4A479].mkv":                                                   Other,
		"[UTW]_Kara_no_Kyoukai_-_Extra_Chorus_[BD][h264-1080p][FLAC][46FCA8E0].mkv":                                                                  Other,

		// The whole series: only its file list can say.
		"[Anime Time] Kara No Kyoukai (The Garden of Sinners) - Full Series [BD][All Movie][1080p][HEVC 10bit x265][AAC]":         Unknown,
		"[ANE] Kara no Kyoukai [BDRip 1080p x264 FLAC]":                                                                           Unknown,
		"[Fortissimo] Kara no Kyoukai/The Garden of Sinners v2 (all movies, HEVC FLAC 2.0 & 5.1)":                                 Unknown,
		"[Trix] Kara no Kyoukai (All Movies) [Eng subs - VOSTFR] (BD 1080p AV1) - The Garden of Sinners":                          Unknown,
		"[Judas] Kara no Kyoukai (The Garden of Sinners) (Complete Movie Series) [BD 1080p][HEVC x265][Multi-Subs] (Movie)":       Unknown,
		"[Arukoru] Kara No Kyoukai (The Garden of Sinners) Movies 1 - 8 & Remix [1080p x265 10bit BD Eng Sub AAC 5.1]":            Unknown,
		"[JPSDR] Kara no Kyoukai [1-7][BDRip][1920x1080 H264 Hi10P DTSHD-MA 5.1]":                                                 Unknown,
		"[UltraRemux] Kara no Kyoukai (The Garden of Sinners) - Complete Series + Manner Movies + OST + Extras (JPBD) (Lossless)": Unknown,
		"[DBD-Raws][剧场版 空之境界/Kara no Kyoukai: The Garden of Sinners/劇場版 空の境界][01-08全集+SP+特典映像][1080P][BDRip][HEVC-10bit]":         Unknown,
	} {
		if got := ch6.Match(name); got != want {
			t.Errorf("%q = %v, want %v", name, got, want)
		}
	}
}

func TestKeyReadsTheChapterAndTheSeriesSize(t *testing.T) {
	if k := keyFor("ch6"); k.Number != 6 || k.Count != len(series) {
		t.Errorf("chapter 6: number %d, count %d", k.Number, k.Count)
	}
	// No title of it states a chapter; a number can then say nothing.
	if k := keyFor("future"); k.Number != 0 {
		t.Errorf("recalled out summer: number %d, want 0", k.Number)
	}
}

func TestNumberReadsFilmNumbersNotTags(t *testing.T) {
	for name, want := range map[string]int{
		"06 Kara No Kyoukai - Boukyaku Rokuon-6":                             6,
		"[ANE] Kara no Kyoukai - 4v0 - The Hollow Shrine [BDRip 1080p x264]": 4,
		"[Fortissimo] Kara no Kyoukai - 5.0 - Paradox Spiral":                5,
		"Show Movie 3 [1080p]":                                               3,
		"[Group] Show - The Movie (BD 1080p AAC 2.0)":                        0,
		"[Group] Show (2019) [1080p][10bit][x265]":                           0,
		"Show Movies 1 - 3 [BD]":                                             0,
		"[ANE] Kara no Kyoukai - Remix v0 - Gate of Seventh Heaven":          0,
	} {
		if got, _ := Number(name); got != want {
			t.Errorf("%q = %d, want %d", name, got, want)
		}
	}
	if _, fractional := Number("06.5 Kara No Kyoukai Remix - Gate Of Seventh Heaven-7"); !fractional {
		t.Error("06.5 was not read as an extra between two films")
	}
}

func TestLoneFileIsTakenUnlessItNamesAnotherFilm(t *testing.T) {
	ch6 := keyFor("ch6")
	if _, ok := ch6.Pick(files(1.5, "[Group] Kara no Kyoukai - Boukyaku Rokuon [1080p].mkv")); !ok {
		t.Error("the film's own single release was refused")
	}
	// Unnamed: a single-film release with nothing to contradict it, as before.
	if _, ok := ch6.Pick(files(1.5, "movie.mkv")); !ok {
		t.Error("an unnamed lone file was refused")
	}
	if _, ok := ch6.Pick(files(1.5, "[Group] Kara no Kyoukai - Fukan Fuukei [1080p].mkv")); ok {
		t.Error("another film's single release was taken as chapter 6")
	}
	if _, ok := ch6.Pick(files(0.001, "readme.txt")); ok {
		t.Error("picked from a release with no video")
	}
}
