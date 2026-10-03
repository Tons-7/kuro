package engine

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"

	"kuro/internal/torrent"
)

// files is the engine's storage: plain files (see Options.CacheDir), opened for each
// read or write and closed after. anacrolix's own file storage memory-maps
// every file for good, and on Windows a mapped file cannot be deleted, which
// is most of what the cache does.
type files struct {
	base   string
	pieces storage.PieceCompletion
}

func (s files) OpenTorrent(_ context.Context, info *metainfo.Info, hash metainfo.Hash) (storage.TorrentImpl, error) {
	t := &fileTorrent{info: info, hash: hash, pieces: s.pieces}
	dir := filepath.Clean(s.base)
	if info.IsDir() {
		dir = filepath.Join(s.base, info.BestName())
	}
	for fi := range info.UpvertedFilesIter() {
		path := filepath.Join(dir, info.BestName())
		if info.IsDir() {
			path = filepath.Join(append([]string{dir}, fi.BestPath()...)...)
		}
		if !torrent.Within(s.base, path) {
			return storage.TorrentImpl{}, errors.New("file path escapes the cache folder")
		}
		t.files = append(t.files, segment{
			path: path, offset: fi.TorrentOffset, length: fi.Length,
			padding: strings.Contains(fi.Attr, "p"),
		})
	}
	return storage.TorrentImpl{
		Piece: func(p metainfo.Piece) storage.PieceImpl { return filePiece{t, p} },
		Close: func() error { return nil },
	}, nil
}

type segment struct {
	path           string
	offset, length int64
	// BEP 47 padding: zeros on the wire, never written to disk.
	padding bool
}

type fileTorrent struct {
	info   *metainfo.Info
	hash   metainfo.Hash
	pieces storage.PieceCompletion
	files  []segment
}

// each calls f for every file part of the torrent range [off, off+n).
func (t *fileTorrent) each(off, n int64, f func(s segment, fileOff, bufOff, length int64) error) error {
	end := off + n
	for _, s := range t.files {
		lo, hi := max(off, s.offset), min(end, s.offset+s.length)
		if lo >= hi {
			continue
		}
		if err := f(s, lo-s.offset, lo-off, hi-lo); err != nil {
			return err
		}
	}
	return nil
}

type filePiece struct {
	t *fileTorrent
	p metainfo.Piece
}

func (fp filePiece) key() metainfo.PieceKey {
	return metainfo.PieceKey{InfoHash: fp.t.hash, Index: fp.p.Index()}
}

func (fp filePiece) ReadAt(b []byte, off int64) (int, error) {
	base := fp.p.Offset() + off
	err := fp.t.each(base, int64(len(b)), func(s segment, fileOff, bufOff, n int64) error {
		dst := b[bufOff : bufOff+n]
		if s.padding {
			clear(dst)
			return nil
		}
		f, err := os.Open(s.path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = f.ReadAt(dst, fileOff)
		if errors.Is(err, io.EOF) {
			return io.ErrUnexpectedEOF
		}
		return err
	})
	if err != nil {
		return 0, err
	}
	return len(b), nil
}

func (fp filePiece) WriteAt(b []byte, off int64) (int, error) {
	base := fp.p.Offset() + off
	err := fp.t.each(base, int64(len(b)), func(s segment, fileOff, bufOff, n int64) error {
		if s.padding {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
			return err
		}
		f, err := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		_, werr := f.WriteAt(b[bufOff:bufOff+n], fileOff)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		return werr
	})
	if err != nil {
		return 0, err
	}
	return len(b), nil
}

func (fp filePiece) MarkComplete() error    { return fp.t.pieces.Set(fp.key(), true) }
func (fp filePiece) MarkNotComplete() error { return fp.t.pieces.Set(fp.key(), false) }

// Completion trusts a record only while the piece's bytes are on disk: the
// cache deletes episodes. With no record and no bytes the piece is plainly
// incomplete; "unknown" would have anacrolix hash every piece of a new torrent.
func (fp filePiece) Completion() storage.Completion {
	c, err := fp.t.pieces.Get(fp.key())
	if err != nil || (c.Ok && !c.Complete) {
		return c
	}
	if !fp.onDisk() {
		return storage.Completion{Ok: true, Complete: false}
	}
	return c
}

// onDisk reports whether every file the piece spans is long enough to hold it.
func (fp filePiece) onDisk() bool {
	ok := true
	fp.t.each(fp.p.Offset(), fp.p.Length(), func(s segment, fileOff, _, n int64) error {
		if s.padding {
			return nil
		}
		if info, err := os.Stat(s.path); err != nil || info.Size() < fileOff+n {
			ok = false
		}
		return nil
	})
	return ok
}
