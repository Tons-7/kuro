package store

import (
	"context"
	"path/filepath"
	"testing"
)

// One unreachable root must not mark another root's library missing, and must
// not stop a healthy root's deletions being noticed either.
func TestSweepLocalOnlyTouchesTheRootsThatWalked(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	anime := filepath.Join("D:", "Anime")
	// A sibling whose name starts with the first root's, which a bare prefix
	// match would sweep as well.
	movies := filepath.Join("D:", "AnimeMovies")
	downloads := filepath.Join("E:", "Downloads")

	kept := filepath.Join(anime, "kept.mkv")
	gone := filepath.Join(anime, "gone.mkv")
	sibling := filepath.Join(movies, "film.mkv")
	unplugged := filepath.Join(downloads, "unplugged.mkv")

	first, _ := s.NextScanStamp(ctx)
	if _, err := s.SaveLocalFiles(ctx, first, []LocalFile{
		{Path: kept, Size: 1, Modified: 1},
		{Path: gone, Size: 1, Modified: 1},
		{Path: sibling, Size: 1, Modified: 1},
		{Path: unplugged, Size: 1, Modified: 1},
	}); err != nil {
		t.Fatal(err)
	}

	// A later scan where only the first root walked.
	next, _ := s.NextScanStamp(ctx)
	if _, err := s.SaveLocalFiles(ctx, next, []LocalFile{
		{Path: kept, Size: 1, Modified: 1},
	}); err != nil {
		t.Fatal(err)
	}
	missing, err := s.SweepLocal(ctx, next, []string{anime})
	if err != nil {
		t.Fatal(err)
	}
	if missing != 1 {
		t.Fatalf("flagged %d files, want just the one deleted under the walked root", missing)
	}

	files, err := s.LocalFiles(ctx, 0, false, Paging{PerPage: 50})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{gone: true, kept: false, sibling: false, unplugged: false}
	for _, f := range files.Items {
		if expected, known := want[f.Path]; known && f.Missing != expected {
			t.Errorf("%s: missing = %v, want %v", f.Path, f.Missing, expected)
		}
	}

	// No roots walked at all: nothing is swept.
	if n, err := s.SweepLocal(ctx, next, nil); err != nil || n != 0 {
		t.Fatalf("swept %d with no roots (err %v)", n, err)
	}
}
