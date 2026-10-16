package store

import (
	"context"
	"testing"

	"kuro/internal/metadata"
)

// An episode a library folder holds needs no download, so "Download all" must not count it.
func TestEpisodesMarkWhatTheLibraryHolds(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedCatalogue(t, s, 1, "FINISHED", 3)
	seedCatalogue(t, s, 2, "FINISHED", 3)
	if _, err := s.SaveEpisodes(ctx, 1, []metadata.Episode{
		{Number: 1, TitleEN: "One"}, {Number: 2, TitleEN: "Two"}, {Number: 3, TitleEN: "Three"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveLocalFiles(ctx, 1, []LocalFile{
		{Path: `D:\Anime\Show - 02.mkv`, Size: 1, AnimeID: 1, Episode: 2, Confidence: 0.9},
		{Path: `D:\Anime\Show - 03.mkv`, Size: 1, AnimeID: 1, Episode: 3, Confidence: 0.9},
		// Show 2 has no episode metadata: its planned rows are flagged too.
		{Path: `D:\Anime\Other - 01.mkv`, Size: 1, AnimeID: 2, Episode: 1, Confidence: 0.9},
	}); err != nil {
		t.Fatal(err)
	}
	gone, err := s.LocalEpisode(ctx, 1, 3)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkLocalMissing(ctx, gone.ID); err != nil {
		t.Fatal(err)
	}

	for animeID, want := range map[int][]bool{1: {false, true, false}, 2: {true, false, false}} {
		eps, err := s.Episodes(ctx, animeID)
		if err != nil {
			t.Fatal(err)
		}
		if len(eps) != len(want) {
			t.Fatalf("show %d: %d episodes, want %d", animeID, len(eps), len(want))
		}
		for i, e := range eps {
			if e.Local != want[i] {
				t.Errorf("show %d episode %d: local = %v, want %v (a missing file holds nothing)", animeID, e.Number, e.Local, want[i])
			}
		}
	}
}
