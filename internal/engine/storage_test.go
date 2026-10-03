package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
)

// A file path climbing out of the cache is refused, including into a sibling
// whose name starts like the cache's.
func TestStorageRefusesPathsOutsideTheCache(t *testing.T) {
	base := filepath.Join(t.TempDir(), "cache")
	info := &metainfo.Info{Name: "pack", PieceLength: 4, Files: []metainfo.FileInfo{
		{Length: 4, Path: []string{"..", "..", "cache2", "x.mkv"}},
	}}
	if _, err := (files{base: base}).OpenTorrent(context.Background(), info, metainfo.Hash{}); err == nil {
		t.Fatal("a path outside the cache was accepted")
	}
}

// A piece's completion: missing bytes are a known "no" (else anacrolix hashes
// every piece of a new torrent), bytes without a record are unknown (carried
// over, so they get hashed), and a record whose file is gone is not trusted.
func TestCompletionFollowsTheDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ep.mkv")
	info := &metainfo.Info{PieceLength: 4, Length: 8, Name: "ep.mkv", Pieces: make([]byte, 40)}
	pieces := storage.NewMapPieceCompletion()
	ft := &fileTorrent{info: info, pieces: pieces, files: []segment{{path: path, length: 8}}}
	piece := filePiece{ft, info.Piece(0)}

	if c := piece.Completion(); !c.Ok || c.Complete {
		t.Errorf("no file, no record = %+v, want known incomplete", c)
	}

	os.WriteFile(path, []byte("12345678"), 0o644)
	if c := piece.Completion(); c.Ok {
		t.Errorf("bytes without a record = %+v, want unknown", c)
	}

	piece.MarkComplete()
	if c := piece.Completion(); !c.Ok || !c.Complete {
		t.Errorf("recorded and on disk = %+v, want complete", c)
	}

	os.Remove(path)
	if c := piece.Completion(); !c.Ok || c.Complete {
		t.Errorf("record outliving its file = %+v, want incomplete", c)
	}
}
