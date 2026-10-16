package bt

import (
	"os"
	"testing"

	"kuro/internal/bt/internal/testutil"
	"kuro/internal/bt/storage"
)

// Hashing went by piece index: a seek's piece waited behind every earlier piece queued before it.
func TestTheMostUrgentPieceIsHashedFirst(t *testing.T) {
	dir, mi := testutil.GreetingTestTorrent()
	defer os.RemoveAll(dir)
	cfg := TestingConfig(t)
	cfg.DefaultStorage = storage.NewFileWithCompletion(t.TempDir(), storage.NewMapPieceCompletion())
	var cl Client
	cl.init(cfg)
	defer cl.Close()
	tor := cl.newTorrent(mi.HashInfoBytes(), storage.NewFileWithCompletion(t.TempDir(), storage.NewMapPieceCompletion()))
	tor.setChunkSize(2)
	cl.lock()
	defer cl.unlock()
	if err := tor.setInfoBytesLocked(mi.InfoBytes); err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		tor.piecesQueuedForHash.Add(i)
	}
	// Setting the info starts hashing pieces 0 and 1; under the lock they can be made free to pick again.
	hashing := [2]bool{tor.piece(0).hashing, tor.piece(1).hashing}
	tor.piece(0).hashing, tor.piece(1).hashing = false, false
	defer func() { tor.piece(0).hashing, tor.piece(1).hashing = hashing[0], hashing[1] }()
	tor.piece(2).priority = PiecePriorityNow
	if got := tor.getPieceToHash(); !got.Ok || got.Value != 2 {
		t.Errorf("hashed %v first; want piece 2, the one a reader waits on", got)
	}
	tor.piece(2).priority = PiecePriorityNone
	if got := tor.getPieceToHash(); !got.Ok || got.Value != 0 {
		t.Errorf("hashed %v first among equals; want piece 0", got)
	}
}
