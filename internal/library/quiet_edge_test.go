package library

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"kuro/internal/db"
	"kuro/internal/store"
	"kuro/internal/torrent"
)

func checkingRqbit(t *testing.T, polls *int, mu *sync.Mutex) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.URL.Path == "/torrents":
			io.WriteString(w, `{"torrents":[{"id":1,"info_hash":"AAAA","name":"a"}]}`)
		case strings.HasSuffix(r.URL.Path, "/stats/v1"):
			*polls++
			io.WriteString(w, `{"state":"initializing","finished":false,"progress_bytes":1,"total_bytes":10000}`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func quietDownloader(t *testing.T, url string) *Downloader {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	conn.Migrate()
	st := store.New(conn)
	return NewDownloader(st, NewPrefetcher(st, nil, torrent.NewClient(url), discard()), nil, discard())
}

// A torrent whose check never ends must not keep the startup pass polling for good.
func TestQuietGivesUpAfterTheDeadline(t *testing.T) {
	var mu sync.Mutex
	polls := 0
	srv := checkingRqbit(t, &polls, &mu)

	wasEvery, wasFor := quietRecheck, quietFor
	quietRecheck, quietFor = 10*time.Millisecond, 60*time.Millisecond
	t.Cleanup(func() { quietRecheck, quietFor = wasEvery, wasFor })

	quietDownloader(t, srv.URL).Quiet(context.Background())
	time.Sleep(200 * time.Millisecond)
	mu.Lock()
	n := polls
	mu.Unlock()
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if polls != n {
		t.Fatalf("still polling after the deadline: %d then %d", n, polls)
	}
	if n < 2 || n > 10 {
		t.Fatalf("%d polls inside a 60ms window at 10ms", n)
	}
}

func TestQuietStopsWhenCancelled(t *testing.T) {
	var mu sync.Mutex
	polls := 0
	srv := checkingRqbit(t, &polls, &mu)
	was := quietRecheck
	quietRecheck = 10 * time.Millisecond
	t.Cleanup(func() { quietRecheck = was })

	ctx, cancel := context.WithCancel(context.Background())
	quietDownloader(t, srv.URL).Quiet(ctx)
	cancel()
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	n := polls
	mu.Unlock()
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if polls != n {
		t.Fatalf("kept polling after cancel: %d then %d", n, polls)
	}
}
