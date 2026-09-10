package score

import (
	"testing"

	"kuro/internal/indexer"
	"kuro/internal/parse"
)

func seeded(title string, size int64, seeders int) Candidate {
	return Candidate{
		Torrent:   indexer.Torrent{Title: title, Size: size, Seeders: seeders, SeedersKnown: true},
		Release:   parse.Parse(title),
		Confirmed: true,
	}
}

// One 1080p crawled and another did not: the swarm decides between equals.
func TestSwarmDecidesBetweenEqualReleases(t *testing.T) {
	prefs := DefaultPreferences()
	thin := seeded("[GroupA] Show - 07 [1080p].mkv", 1<<30, 3)
	healthy := seeded("[GroupB] Show - 07 [1080p].mkv", 1<<30, 60)

	ranked := Rank([]Candidate{thin, healthy}, prefs)
	if ranked[0].Torrent.Seeders != 60 {
		t.Errorf("picked the %d-seeder release over the 60", ranked[0].Torrent.Seeders)
	}
}

// Quality still comes first: a well-seeded 720p must not beat a 1080p.
func TestSwarmNeverBuysALowerResolution(t *testing.T) {
	prefs := DefaultPreferences()
	small := seeded("[GroupA] Show - 07 [720p].mkv", 700<<20, 900)
	big := seeded("[GroupB] Show - 07 [1080p].mkv", 1<<30, 6)

	ranked := Rank([]Candidate{small, big}, prefs)
	if ranked[0].Release.Resolution != "1080p" {
		t.Errorf("a %d-seeder 720p outranked the 1080p", small.Torrent.Seeders)
	}
}

// A pack stays a fallback however well seeded.
func TestABigSwarmDoesNotPromoteABatch(t *testing.T) {
	prefs := DefaultPreferences()
	single := seeded("[GroupA] Show - 07 [1080p].mkv", 1<<30, 4)
	batch := seeded("[GroupB] Show (01-170) [1080p][Batch]", 200<<30, 300)
	batch.TotalEpisodes = 170

	ranked := Rank([]Candidate{batch, single}, prefs)
	if ranked[0].Release.Batch {
		t.Errorf("the batch won: %s", ranked[0].Torrent.Title)
	}
}

// The cap is written for a 24-minute episode; a film is several times that.
func TestSizeLimitFollowsRuntime(t *testing.T) {
	prefs := DefaultPreferences()
	film := seeded("[Group] The Garden of Sinners 1 [BD 1080p].mkv", 3400<<20, 42)

	if _, ok := Best([]Candidate{film}, prefs); ok {
		t.Error("a 3.4 GB file passed the 3 GiB cap with no runtime known")
	}

	film.RuntimeMinutes = 50
	best, ok := Best([]Candidate{film}, prefs)
	if !ok {
		t.Fatalf("a 50-minute film was still refused: %v", first(Rank([]Candidate{film}, prefs)).Blocked)
	}
	if best.Torrent.Size != 3400<<20 {
		t.Errorf("picked %d bytes", best.Torrent.Size)
	}

	// Not a blank cheque: a 24-minute episode keeps the plain limit.
	episode := seeded("[Group] Show - 07 [1080p].mkv", 3400<<20, 42)
	episode.RuntimeMinutes = 24
	if _, ok := Best([]Candidate{episode}, prefs); ok {
		t.Error("an oversized episode passed on a film's allowance")
	}
}

func first(rs []Result) Result {
	if len(rs) == 0 {
		return Result{}
	}
	return rs[0]
}

// A dead .avi naming the episode used to win on that alone, over the complete
// Blu-ray, because nothing else stated the episode.
func TestUnlabelledEpisodeLosesToALabelledPack(t *testing.T) {
	prefs := DefaultPreferences()
	avi := Candidate{
		Torrent:   indexer.Torrent{Title: "[Animanda] Death Note - 12 [400AF72B].avi"},
		Release:   parse.Parse("[Animanda] Death Note - 12 [400AF72B].avi"),
		Confirmed: true,
	}
	pack := seeded("[AnimeRG] Death Note Bluray The Complete Series [1080p] [Dual Audio] [HEVC]", 40<<30, 25)
	pack.Confirmed = false
	pack.TotalEpisodes = 37

	ranked := Rank([]Candidate{avi, pack}, prefs)
	if ranked[0].Release.Extension == "avi" {
		t.Errorf("the unlabelled .avi won: %s", ranked[0].Torrent.Title)
	}
}

// The same rule must not hand a pack the win when the episode is labelled.
func TestLabelledEpisodeStillBeatsThePack(t *testing.T) {
	prefs := DefaultPreferences()
	single := seeded("[GroupA] Show - 07 [1080p].mkv", 1<<30, 5)
	pack := seeded("[GroupB] Show S01 [1080p][BD][Dual Audio]", 40<<30, 400)
	pack.Confirmed = false
	pack.TotalEpisodes = 25

	ranked := Rank([]Candidate{pack, single}, prefs)
	if ranked[0].Release.Batch {
		t.Errorf("the pack won: %s", ranked[0].Torrent.Title)
	}
}
