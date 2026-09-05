package library

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"kuro/internal/db"
	"kuro/internal/indexer"
	"kuro/internal/score"
	"kuro/internal/store"
)

const slimeS3 = 156822

func slimeStore(t *testing.T) *store.Store {
	t.Helper()

	conn, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := conn.Migrate(); err != nil {
		t.Fatal(err)
	}
	st := store.New(conn)

	english, tv, episodes := "That Time I Got Reincarnated as a Slime Season 3", "TV", 24
	if _, err := st.ImportList(context.Background(), []store.Anime{{
		ID: slimeS3, Romaji: "Tensei shitara Slime Datta Ken 3rd Season", English: &english,
		Format: &tv, Episodes: &episodes, Synonyms: "[]", Genres: "[]",
	}}, nil, store.ImportMerge); err != nil {
		t.Fatal(err)
	}
	return st
}

// Earlier-season packs beside the real season-three releases.
var slimeReleases = []indexer.Torrent{
	release(strings.Repeat("a", 40), "[Trix] Tensei Shitara Slime Datta Ken S01+02+OADs+Tensura Nikki - AV1 MiNi", 900),
	release(strings.Repeat("b", 40), "[Judas] Tensei Shitara Slime Datta Ken (That Time I Got Reincarnated as a Slime) S02SP01-02 [1080p][HEVC x265 10bit][Multi-Subs] (Weekly)", 400),
	release(strings.Repeat("c", 40), "[Erai-raws] Tensei Shitara Slime Datta Ken 3rd Season - 02 [1080p][HEVC][Multiple Subtitle][ABCD1234].mkv", 200),
	release(strings.Repeat("d", 40), "[SubsPlease] Tensei Shitara Slime Datta Ken S3 - 02 (1080p) [ABCD1234].mkv", 150),
}

func TestFindWithoutASeasonTakesItFromTheTitle(t *testing.T) {
	st := slimeStore(t)
	f := NewFinder(st, fixedIndexer{results: slimeReleases}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	found, err := f.Find(context.Background(), Request{
		AnimeID: slimeS3, Episode: 2, Prefs: score.DefaultPreferences(),
	})
	if err != nil {
		t.Fatal(err)
	}

	var got []string
	for _, r := range found.Results {
		got = append(got, r.Release.Group)
	}
	if len(got) != 2 || got[0] == "Trix" || got[1] == "Trix" || got[0] == "Judas" || got[1] == "Judas" {
		t.Fatalf("results = %v, want only the two season-three releases", got)
	}
	if found.Best == nil || found.Best.Release.Season != 3 || found.Best.Release.Episode != 2 {
		t.Errorf("best = %+v, want a season 3 episode 2 release", found.Best)
	}
}

func TestCandidatesWithoutASeasonNeverPickAnEarlierSeasonPack(t *testing.T) {
	st := slimeStore(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	p := NewPlayback(st, NewFinder(st, fixedIndexer{results: slimeReleases}, log), nil, nil, t.TempDir(), log)

	cands, err := p.candidates(context.Background(), PlayRequest{
		AnimeID: slimeS3, Episode: 2, Prefs: score.DefaultPreferences(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) == 0 {
		t.Fatal("no candidates")
	}
	for _, c := range cands {
		if c.Release.Season != 3 {
			t.Errorf("candidate %q is not season three", c.Torrent.Title)
		}
	}
}

// What /api/play used to do; documents the failure.
func TestForcingSeasonOneIsWhatBroke(t *testing.T) {
	st := slimeStore(t)
	f := NewFinder(st, fixedIndexer{results: slimeReleases}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	found, err := f.Find(context.Background(), Request{
		AnimeID: slimeS3, Episode: 2, Season: 1, Prefs: score.DefaultPreferences(),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range found.Results {
		if r.Release.Season == 3 {
			t.Fatalf("%q verified under a forced season 1; this test documents the old failure", r.Torrent.Title)
		}
	}
}
