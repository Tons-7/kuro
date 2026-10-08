package score

import (
	"testing"

	"kuro/internal/film"
)

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

// The first refusing tier is what gets reported; a dead swarm should not be explained as a size problem.
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

// Patch files and subtitle packs are listed under the show's name; a title ending in a video's name is not one.
func TestNonVideoReleasesAreRefused(t *testing.T) {
	prefs := DefaultPreferences()
	for _, title := range []string{
		"(project-gxs)_Kara_no_Kyoukai_-_06_-_Oblivion_Recording_v2_(10bit_BD_1080p)_(ANE).gxs",
		"[Group] Show - 01 [1080p] subtitles.zip",
		"Show OST.flac",
	} {
		if r := Rank([]Candidate{candidate(title, 20, 1<<30)}, prefs)[0]; r.AutoPick || r.Blocked != "not a video file" {
			t.Errorf("%q: autoPick %v, blocked %q", title, r.AutoPick, r.Blocked)
		}
	}
	for _, title := range []string{
		"[UTW]_Kara_no_Kyoukai_-_Mirai_Fukuin_[BD][h264-1080p_FLAC][76F03FE0].mkv",
		"[Group] Show - 01 [1080p]",
		"[Group] Show v2.0 - 01 [1080p]",
	} {
		if r := Rank([]Candidate{candidate(title, 20, 1<<30)}, prefs)[0]; r.Blocked == "not a video file" {
			t.Errorf("%q was refused as not a video", title)
		}
	}
}

// One film of a series inside a batch of all of them costs its share, not the whole batch.
func TestFilmBatchIsSizedAsOneFilm(t *testing.T) {
	key := film.New(film.Entry{Titles: []string{"Series: One"}}, []film.Entry{{Titles: []string{"Series: Two"}}, {Titles: []string{"Series: Three"}}})
	batch := candidate("[Group] Series (All Movies) [1080p]", 20, 30<<30)
	batch.Film, batch.Release.Batch = &key, true
	if got := batch.EpisodeBytes(); got != 10<<30 {
		t.Errorf("a third of 30 GB = %d", got)
	}
	single := candidate("[Group] Series - One [1080p]", 20, 4<<30)
	single.Film = &key
	if got := single.EpisodeBytes(); got != 4<<30 {
		t.Errorf("the film's own release = %d, want its full size", got)
	}
}

// In a film series a one-seeder single release is the last resort, behind a healthy batch.
func TestThinFilmReleaseRanksBehindAHealthyBatch(t *testing.T) {
	key := film.New(film.Entry{Titles: []string{"Series: One"}}, []film.Entry{{Titles: []string{"Series: Two"}}})
	single := candidate("[Group] Series - One [1080p]", 1, 1<<30)
	single.Film, single.Confirmed = &key, true
	batch := candidate("[Group] Series (All Movies) [1080p]", 40, 4<<30)
	batch.Film, batch.Confirmed, batch.Release.Batch = &key, true, true

	if got := Rank([]Candidate{single, batch}, DefaultPreferences()); got[0].Torrent.Seeders != 40 {
		t.Errorf("the one-seeder release ranked first: %s", got[0].Torrent.Title)
	}
	// Outside a film series a single episode still beats a pack, however thin.
	single.Film, batch.Film = nil, nil
	if got := Rank([]Candidate{single, batch}, DefaultPreferences()); got[0].Torrent.Seeders != 1 {
		t.Errorf("a pack ranked above the single episode: %s", got[0].Torrent.Title)
	}
}
