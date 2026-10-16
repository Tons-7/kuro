package engine

import (
	"io"
	"time"

	"kuro/internal/bt"
)

// readWait is a stream read in progress: the piece it needs, since when, and whether a no-holder line went out.
type readWait struct {
	piece    int
	since    time.Time
	reported bool
}

// watched tells the janitor which piece each read waits on, so a read stuck behind a silent peer is rescued.
type watched struct {
	bt.Reader
	e        *Engine
	en       *entry
	offset   int64 // the file's place in the torrent
	pieceLen int64
	pos      int64
}

func (w *watched) Read(p []byte) (int, error) {
	w.e.mu.Lock()
	if w.en.waits == nil {
		w.en.waits = map[*watched]readWait{}
	}
	w.en.waits[w] = readWait{piece: int((w.offset + w.pos) / w.pieceLen), since: time.Now()}
	w.e.mu.Unlock()
	n, err := w.Reader.Read(p)
	w.done()
	w.pos += int64(n)
	return n, err
}

func (w *watched) Seek(offset int64, whence int) (int64, error) {
	pos, err := w.Reader.Seek(offset, whence)
	if err == nil {
		w.pos = pos
	}
	return pos, err
}

func (w *watched) done() {
	w.e.mu.Lock()
	delete(w.en.waits, w)
	w.e.mu.Unlock()
}

var _ io.ReadSeeker = (*watched)(nil)
