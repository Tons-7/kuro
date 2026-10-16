package library

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"kuro/internal/match"
	"kuro/internal/store"
)

func scanned(t *testing.T, s *Scanner) ScanReport {
	t.Helper()
	rep, err := s.Scan(context.Background(), testIndex())
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

// A folder inside a root that cannot be read keeps its rows: sweeping them would drop hand assignments.
func TestUnreadableSubfolderKeepsItsRows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("uses icacls to deny access")
	}
	s, st := newScanner(t)
	root := t.TempDir()
	setRoots(t, st, root)
	show := filepath.Join(root, "Show X")
	path := filepath.Join(show, "[SubsPlease] Sousou no Frieren - 10 (1080p).mkv")
	write(t, path, minEpisodeBytes+1)
	scanned(t, s)
	f, _ := st.LocalEpisode(context.Background(), 154587, 10)
	if f.ID == 0 {
		t.Fatal("not recorded")
	}

	user := os.Getenv("USERNAME")
	if out, err := exec.Command("icacls", show, "/deny", user+":(R)").CombinedOutput(); err != nil {
		t.Skipf("icacls: %v %s", err, out)
	}
	t.Cleanup(func() { exec.Command("icacls", show, "/remove:d", user).Run() })

	rep := scanned(t, s)
	if rep.Missing != 0 || len(rep.Errors) == 0 {
		t.Fatalf("report = %+v, want the folder reported and nothing marked missing", rep)
	}
	if f, _ := st.LocalEpisode(context.Background(), 154587, 10); f.ID == 0 {
		t.Fatal("the file under the unreadable folder was marked missing")
	}
	if s.LastReport() == nil || len(s.LastReport().Errors) == 0 {
		t.Fatal("the last report is not kept for the page")
	}
}

// A root that is a junction or symlink is walked where it points.
func TestJunctionRootIsWalked(t *testing.T) {
	s, st := newScanner(t)
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "Anime")
	if runtime.GOOS == "windows" {
		if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, real).CombinedOutput(); err != nil {
			t.Skipf("mklink: %v %s", err, out)
		}
	} else if err := os.Symlink(real, link); err != nil {
		t.Skip(err)
	}
	setRoots(t, st, link)
	write(t, filepath.Join(real, "[SubsPlease] Sousou no Frieren - 10 (1080p).mkv"), minEpisodeBytes+1)

	if rep := scanned(t, s); rep.Video != 1 || rep.Matched != 1 {
		t.Fatalf("report = %+v, want the file behind the link", rep)
	}
	// Seen again: still there, not missing.
	if rep := scanned(t, s); rep.Missing != 0 {
		t.Fatalf("second scan marked %d missing", rep.Missing)
	}
}

// Downloads in kuro's own cache folder are not library files, however the roots are drawn.
func TestScanSkipsKurosCacheFolder(t *testing.T) {
	s, st := newScanner(t)
	root := t.TempDir()
	cache := filepath.Join(root, "kuro", "cache")
	s.WithCacheDir(cache)
	setRoots(t, st, root)
	write(t, filepath.Join(cache, "[SubsPlease] Sousou no Frieren - 10 (1080p).mkv"), minEpisodeBytes+1)
	write(t, filepath.Join(root, "[SubsPlease] Sousou no Frieren - 11 (1080p).mkv"), minEpisodeBytes+1)

	if rep := scanned(t, s); rep.Video != 1 {
		t.Fatalf("saw %d videos, want the one outside the cache", rep.Video)
	}
}

// A folder taken out of the library takes its files with it.
func TestRemovedRootRetiresItsFiles(t *testing.T) {
	s, st := newScanner(t)
	a, b := t.TempDir(), t.TempDir()
	setRoots(t, st, a, b)
	write(t, filepath.Join(a, "[SubsPlease] Sousou no Frieren - 10 (1080p).mkv"), minEpisodeBytes+1)
	write(t, filepath.Join(b, "[SubsPlease] Sousou no Frieren - 11 (1080p).mkv"), minEpisodeBytes+1)
	scanned(t, s)

	setRoots(t, st, a)
	if rep := scanned(t, s); rep.Missing != 1 {
		t.Fatalf("missing = %d, want the file under the removed folder", rep.Missing)
	}
	if f, _ := st.LocalEpisode(context.Background(), 154587, 11); f.ID != 0 {
		t.Fatal("a file under a removed folder still plays")
	}
	if f, _ := st.LocalEpisode(context.Background(), 154587, 10); f.ID == 0 {
		t.Fatal("the kept folder's file went too")
	}
}

// A file that matches two seasons alike is left for a hand assignment; the folder's season breaks the tie.
func TestAmbiguousMatchIsNotRecordedAsFact(t *testing.T) {
	ix := match.NewIndex()
	ix.Add(match.Media{ID: 1, Titles: []string{"Frieren"}, Season: 1})
	ix.Add(match.Media{ID: 2, Titles: []string{"Frieren"}, Season: 2})
	bare := &store.LocalFile{Path: filepath.Join("lib", "Stuff", "[Group] Frieren - 05.mkv")}
	if identify(bare, ix, nil) {
		t.Fatalf("a toss-up was recorded as %d", bare.AnimeID)
	}
	if bare.Title == "" {
		t.Fatal("the parsed title was dropped; the file could not be assigned by hand")
	}
	inSeason := &store.LocalFile{Path: filepath.Join("lib", "Frieren S2", "[Group] Frieren - 05.mkv")}
	if !identify(inSeason, ix, nil) || inSeason.AnimeID != 2 {
		t.Fatalf("the folder's season did not settle it: %+v", inSeason)
	}
}

// Among copies of one episode the sharper encode plays, after any hand assignment.
func TestLocalEpisodePrefersTheSharperCopy(t *testing.T) {
	_, st := newScanner(t)
	ctx := context.Background()
	st.EnsureAnime(ctx, 1)
	if _, err := st.SaveLocalFiles(ctx, 1, []store.LocalFile{
		{Path: "a/Show S01E05 720p.mkv", Size: 2 << 30, AnimeID: 1, Episode: 5, Confidence: 0.6, Resolution: "720p"},
		{Path: "b/Show - 05 1080p.mkv", Size: 1 << 30, AnimeID: 1, Episode: 5, Confidence: 0.5, Resolution: "1080p"},
	}); err != nil {
		t.Fatal(err)
	}
	if f, _ := st.LocalEpisode(ctx, 1, 5); f.Resolution != "1080p" {
		t.Fatalf("picked %+v, want the 1080p copy", f)
	}
	if _, err := st.SaveLocalFiles(ctx, 2, []store.LocalFile{
		{Path: "a/Show S01E05 720p.mkv", Size: 2 << 30, AnimeID: 1, Episode: 5, Confidence: 1, Resolution: "720p"},
	}); err != nil {
		t.Fatal(err)
	}
	if f, _ := st.LocalEpisode(ctx, 1, 5); f.Resolution != "720p" {
		t.Fatalf("picked %+v, want the hand-assigned copy", f)
	}
}
