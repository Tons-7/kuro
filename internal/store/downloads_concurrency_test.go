package store

import (
	"context"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"kuro/internal/db"
)

// A second query while the rows cursor is open holds two connections from the
// read pool, so enough callers at once deadlock every one of them.
func TestDownloadStatusUnderConcurrency(t *testing.T) {
	conn, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := conn.Migrate(); err != nil {
		t.Fatal(err)
	}
	s := New(conn)

	if err := s.RecordTorrent(context.Background(), TorrentRecord{
		InfoHash: "abc", Name: "Show", FileIndex: 1, TotalSize: 1 << 30,
	}); err != nil {
		t.Fatal(err)
	}

	// Comfortably more callers than the pool has connections.
	callers := max(4, runtime.NumCPU()) * 4
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	errs := make(chan error, callers)
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.DownloadStatus(ctx); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)

	var failed int
	for err := range errs {
		if failed == 0 {
			t.Errorf("first failure: %v", err)
		}
		failed++
	}
	if failed > 0 {
		t.Fatalf("%d of %d concurrent calls failed", failed, callers)
	}
}
