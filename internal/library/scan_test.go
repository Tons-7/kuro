package library

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"kuro/internal/db"
	"kuro/internal/match"
	"kuro/internal/store"
)

func newScanner(t *testing.T) (*Scanner, *store.Store) {
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
	return NewScanner(st, slog.New(slog.NewTextHandler(io.Discard, nil))), st
}

// write creates a file large enough to count as an episode.
func write(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
}

func setRoots(t *testing.T, s *store.Store, roots ...string) {
	t.Helper()
	encoded, _ := json.Marshal(roots)
	if err := s.SetSetting(context.Background(), "library.paths", string(encoded)); err != nil {
		t.Fatal(err)
	}
}

func testIndex() *match.Index {
	ix := match.NewIndex()
	ix.Add(match.Media{
		ID:     154587,
		Titles: []string{"Sousou no Frieren", "Frieren: Beyond Journey's End"},
	})
	ix.Add(match.Media{ID: 16498, Titles: []string{"Shingeki no Kyojin", "Attack on Titan"}})
	return ix
}

func TestScanFindsAndMatchesEpisodes(t *testing.T) {
	s, st := newScanner(t)
	root := t.TempDir()
	setRoots(t, st, root)

	write(t, filepath.Join(root, "[SubsPlease] Sousou no Frieren - 10 (1080p) [ABC123].mkv"), minEpisodeBytes+1)
	write(t, filepath.Join(root, "[SubsPlease] Sousou no Frieren - 11 (1080p) [DEF456].mkv"), minEpisodeBytes+1)

	rep, err := s.Scan(context.Background(), testIndex())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Video != 2 {
		t.Fatalf("found %d video files, want 2", rep.Video)
	}
	if rep.Matched != 2 {
		t.Fatalf("matched %d, want 2 (unmatched %d)", rep.Matched, rep.Unmatched)
	}

	f, err := st.LocalEpisode(context.Background(), 154587, 10)
	if err != nil {
		t.Fatal(err)
	}
	if f.ID == 0 {
		t.Fatal("episode 10 not resolvable")
	}
	if f.Resolution != "1080p" || f.Group != "SubsPlease" {
		t.Errorf("parsed fields = %+v", f)
	}
}

// Samples and trailers are video files too, and playing one instead of the
// episode would be worse than not finding anything.
func TestScanSkipsSmallFilesAndExtras(t *testing.T) {
	s, st := newScanner(t)
	root := t.TempDir()
	setRoots(t, st, root)

	write(t, filepath.Join(root, "[SubsPlease] Sousou no Frieren - 10 (1080p).mkv"), minEpisodeBytes+1)
	write(t, filepath.Join(root, "sample.mkv"), 1024)
	write(t, filepath.Join(root, "Extras", "interview.mkv"), minEpisodeBytes+1)
	write(t, filepath.Join(root, "cover.jpg"), minEpisodeBytes+1)

	rep, err := s.Scan(context.Background(), testIndex())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Video != 1 {
		t.Fatalf("kept %d files, want only the episode", rep.Video)
	}
}

// Bare episode filenames are common; the folder carries the show name.
func TestScanFallsBackToFolderName(t *testing.T) {
	s, st := newScanner(t)
	root := t.TempDir()
	setRoots(t, st, root)

	write(t, filepath.Join(root, "Shingeki no Kyojin", "05.mkv"), minEpisodeBytes+1)

	rep, err := s.Scan(context.Background(), testIndex())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Matched != 1 {
		t.Fatalf("matched %d, want 1 from the folder name", rep.Matched)
	}

	f, _ := st.LocalEpisode(context.Background(), 16498, 5)
	if f.ID == 0 {
		t.Fatal("folder-named episode not resolvable")
	}
}

// An unrecognised show still has to be recorded so it can be assigned by hand.
func TestUnmatchedFilesAreKept(t *testing.T) {
	s, st := newScanner(t)
	root := t.TempDir()
	setRoots(t, st, root)

	write(t, filepath.Join(root, "Some Show Nobody Indexed - 03.mkv"), minEpisodeBytes+1)

	rep, err := s.Scan(context.Background(), testIndex())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Unmatched != 1 {
		t.Fatalf("unmatched = %d, want 1", rep.Unmatched)
	}

	page, err := st.LocalFiles(context.Background(), 0, true, store.Paging{Page: 1, PerPage: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("%d unmatched files listed", len(page.Items))
	}

	// Assigning by hand makes it playable.
	if err := st.AssignLocalFile(context.Background(), page.Items[0].ID, 154587, 3); err != nil {
		t.Fatal(err)
	}
	if f, _ := st.LocalEpisode(context.Background(), 154587, 3); f.ID == 0 {
		t.Fatal("assigned file not resolvable")
	}
}

// A deleted file must stop being offered, without erasing the record: an
// unplugged drive should not wipe the library.
func TestRescanMarksDeletedFilesMissing(t *testing.T) {
	s, st := newScanner(t)
	root := t.TempDir()
	setRoots(t, st, root)

	path := filepath.Join(root, "[SubsPlease] Sousou no Frieren - 10 (1080p).mkv")
	write(t, path, minEpisodeBytes+1)

	if _, err := s.Scan(context.Background(), testIndex()); err != nil {
		t.Fatal(err)
	}
	if f, _ := st.LocalEpisode(context.Background(), 154587, 10); f.ID == 0 {
		t.Fatal("first scan did not record the file")
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	rep, err := s.Scan(context.Background(), testIndex())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Missing != 1 {
		t.Fatalf("missing = %d, want 1", rep.Missing)
	}
	if f, _ := st.LocalEpisode(context.Background(), 154587, 10); f.ID != 0 {
		t.Error("a deleted file is still offered for playback")
	}

	stats, _ := st.LocalStats(context.Background())
	if stats.Missing != 1 {
		t.Errorf("stats.Missing = %d", stats.Missing)
	}
	if stats.Files != 0 {
		t.Errorf("stats.Files = %d, want the missing one excluded", stats.Files)
	}
}

func TestScanWithoutConfiguredRootsIsANoOp(t *testing.T) {
	s, _ := newScanner(t)

	rep, err := s.Scan(context.Background(), testIndex())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Video != 0 || len(rep.Roots) != 0 {
		t.Fatalf("report = %+v", rep)
	}
}

// A root that does not exist is reported, not fatal: one bad path must not
// stop the others being scanned.
func TestUnreadableRootIsReportedNotFatal(t *testing.T) {
	s, st := newScanner(t)
	good := t.TempDir()
	setRoots(t, st, good, filepath.Join(good, "does-not-exist"))

	write(t, filepath.Join(good, "[SubsPlease] Sousou no Frieren - 10 (1080p).mkv"), minEpisodeBytes+1)

	rep, err := s.Scan(context.Background(), testIndex())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Video != 1 {
		t.Fatalf("video = %d, want the good root still scanned", rep.Video)
	}
	if len(rep.Errors) != 1 {
		t.Errorf("errors = %v, want the missing root reported", rep.Errors)
	}
}

func TestRescanUpdatesRatherThanDuplicates(t *testing.T) {
	s, st := newScanner(t)
	root := t.TempDir()
	setRoots(t, st, root)

	write(t, filepath.Join(root, "[SubsPlease] Sousou no Frieren - 10 (1080p).mkv"), minEpisodeBytes+1)
	for range 3 {
		if _, err := s.Scan(context.Background(), testIndex()); err != nil {
			t.Fatal(err)
		}
	}

	stats, _ := st.LocalStats(context.Background())
	if stats.Files != 1 {
		t.Fatalf("stats.Files = %d after three scans, want 1", stats.Files)
	}
}
