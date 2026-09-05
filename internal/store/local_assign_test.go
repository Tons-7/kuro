package store

import (
	"context"
	"testing"
)

// A file matched by hand must keep that match through every later scan,
// whatever the parser makes of its name.
func TestRescanKeepsHandAssignments(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	s.ImportList(ctx, []Anime{{ID: 1, Romaji: "Show"}, {ID: 2, Romaji: "Other"}}, nil, ImportMerge)

	first, _ := s.NextScanStamp(ctx)
	if _, err := s.SaveLocalFiles(ctx, first, []LocalFile{
		{Path: `D:\lib\mystery.mkv`, Size: 1, Modified: 1},
		{Path: `D:\lib\guessed.mkv`, Size: 1, Modified: 1, AnimeID: 2, Episode: 3, Confidence: 0.7},
	}); err != nil {
		t.Fatal(err)
	}
	files, _ := s.LocalFiles(ctx, 0, false, Paging{Page: 1, PerPage: 10})
	var mystery int
	for _, f := range files.Items {
		if f.Path == `D:\lib\mystery.mkv` {
			mystery = f.ID
		}
	}
	if err := s.AssignLocalFile(ctx, mystery, 1, 7); err != nil {
		t.Fatal(err)
	}

	// The next scan parses both names again and guesses differently.
	second, _ := s.NextScanStamp(ctx)
	if _, err := s.SaveLocalFiles(ctx, second, []LocalFile{
		{Path: `D:\lib\mystery.mkv`, Size: 2, Modified: 2},
		{Path: `D:\lib\guessed.mkv`, Size: 2, Modified: 2, AnimeID: 1, Episode: 4, Confidence: 0.8},
	}); err != nil {
		t.Fatal(err)
	}

	f, _ := s.LocalFile(ctx, mystery)
	if f.AnimeID != 1 || f.Episode != 7 || f.Confidence != 1 || f.Size != 2 {
		t.Fatalf("hand-assigned file after rescan = %+v", f)
	}
	if g, _ := s.LocalEpisode(ctx, 1, 4); g.ID == 0 {
		t.Fatal("a scanner guess should still follow the latest scan")
	}
	if g, _ := s.LocalEpisode(ctx, 2, 3); g.ID != 0 {
		t.Fatal("the old guess should have been replaced")
	}
}
