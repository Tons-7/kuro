package engine

import (
	"bytes"
	"crypto/rand"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
)

// A single-file release lands straight in the cache, as rqbit put it.
func TestSingleFileTorrentLayout(t *testing.T) {
	seedDir := t.TempDir()
	body := make([]byte, 333_333)
	rand.Read(body)
	name := "[Group] Show - 05 (1080p).mkv"
	os.WriteFile(filepath.Join(seedDir, name), body, 0o644)
	info := metainfo.Info{PieceLength: 32 << 10}
	if err := info.BuildFromFilePath(filepath.Join(seedDir, name)); err != nil {
		t.Fatal(err)
	}
	var mi metainfo.MetaInfo
	mi.InfoBytes, _ = bencode.Marshal(info)
	s := seeder(t, seedDir, mi)

	f := &fixture{cache: t.TempDir(), session: t.TempDir(),
		peer: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: s.LocalPort()}}
	f.start(t)
	c := ctx(t)
	magnet := mi.Magnet(nil, nil).String()
	in, err := f.client.Inspect(c, magnet)
	if err != nil {
		t.Fatal(err)
	}
	added, err := f.client.Add(c, magnet, in.Details.Files[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := f.client.Await(c, added.ID, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	d, _ := f.client.Details(c, added.ID)
	if d.OutputFolder != filepath.Clean(f.cache) || d.Files[0].Name != name {
		t.Fatalf("details = %+v", d)
	}
	disk, err := os.ReadFile(filepath.Join(f.cache, name))
	if err != nil || !bytes.Equal(disk, body) {
		t.Fatalf("file on disk: %v", err)
	}

	// Prewarm reads the tail and the head; both must arrive.
	if err := f.client.Prewarm(c, added.ID, 0, 64<<10); err != nil {
		t.Fatalf("prewarm: %v", err)
	}
	if err := f.client.PrioritiseAt(c, added.ID, 0, 0.5); err != nil {
		t.Fatalf("prioritise: %v", err)
	}
}

// A file deleted behind the engine's back is no longer reported complete.
func TestDeletedFileIsNoLongerComplete(t *testing.T) {
	f := newFixture(t)
	c := ctx(t)
	in, _ := f.client.Inspect(c, f.magnet)
	added, err := f.client.Add(c, f.magnet, in.Details.Files[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := f.client.Await(c, added.ID, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	f.engine.Stop()
	os.Remove(filepath.Join(f.cache, "Show Pack", "Show - 01.mkv"))

	f.peer = &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1}
	f.start(t)
	live, _ := f.client.Live(c)
	for _, id := range live {
		f.client.WaitLive(c, id, 10*time.Second)
		if s, _ := f.client.Stats(c, id); s.Finished {
			t.Fatalf("a deleted episode still reads as downloaded: %+v", s)
		}
	}
}

func TestForgetKeepsFiles(t *testing.T) {
	f := newFixture(t)
	c := ctx(t)
	in, _ := f.client.Inspect(c, f.magnet)
	added, _ := f.client.Add(c, f.magnet, in.Details.Files[1])
	if err := f.client.Await(c, added.ID, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := f.client.Forget(c, added.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(f.cache, "Show Pack", "Show - 02.mkv")); err != nil {
		t.Errorf("forget removed the file: %v", err)
	}
	if live, _ := f.client.Live(c); len(live) != 0 {
		t.Errorf("still listed: %v", live)
	}
}

// A file deleted behind the engine's back (orphan cleanup after a forget) must
// not leave "complete" records for when the torrent is added again.
func TestReaddAfterDeletedFileForgetsItsPieces(t *testing.T) {
	f := newFixture(t)
	c := ctx(t)
	in, _ := f.client.Inspect(c, f.magnet)
	added, _ := f.client.Add(c, f.magnet, in.Details.Files[1])
	if err := f.client.Await(c, added.ID, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	en, _ := f.engine.entryByID(added.ID)
	file := en.t.Files()[1]
	key := metainfo.PieceKey{InfoHash: en.t.InfoHash(), Index: file.BeginPieceIndex()}
	if got, _ := f.engine.pieces.Get(key); !got.Complete {
		t.Fatal("downloaded piece not recorded")
	}

	f.client.Forget(c, added.ID)
	os.Remove(filepath.Join(f.cache, "Show Pack", "Show - 02.mkv"))
	// An inspection has the metadata and downloads nothing, so no piece is
	// re-fetched under the check; add runs the same cleanup on it.
	if _, err := f.client.Inspect(c, f.magnet); err != nil {
		t.Fatal(err)
	}
	f.engine.mu.Lock()
	again := f.engine.inspected[en.t.InfoHash()].t
	f.engine.mu.Unlock()
	f.engine.forgetMissing(again)
	if got, _ := f.engine.pieces.Get(key); got.Complete {
		t.Error("a deleted file's piece is still recorded complete")
	}
}

// Startup pauses part-downloaded torrents and leaves finished ones alone.
func TestPauseUnfinished(t *testing.T) {
	f := newFixture(t)
	c := ctx(t)
	in, _ := f.client.Inspect(c, f.magnet)
	done, _ := f.client.Add(c, f.magnet, in.Details.Files[0])
	if err := f.client.Await(c, done.ID, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	paused, kept, checking, err := f.client.PauseUnfinished(c, nil)
	if err != nil || paused != 0 || kept != 0 || len(checking) != 0 {
		t.Fatalf("finished torrent touched: paused=%d kept=%d checking=%v err=%v", paused, kept, checking, err)
	}
	if s, _ := f.client.Stats(c, done.ID); s.State != "live" {
		t.Fatalf("state %q", s.State)
	}
}
