package library

import (
	"context"
	"fmt"
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

// rqbit re-hashes files after a launch; a torrent mid-check is neither paused
// nor downloading. The startup pass must not pause it (that froze finished
// episodes at a tenth done) and must come back for it once the check ends.
func TestQuietRevisitsTorrentsStillChecking(t *testing.T) {
	var mu sync.Mutex
	polls := 0
	var paused []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.URL.Path == "/torrents":
			io.WriteString(w, `{"torrents":[{"id":1,"info_hash":"AAAA","name":"a"}]}`)
		case strings.HasSuffix(r.URL.Path, "/stats/v1"):
			polls++
			// Two passes of checking, then the file turns out incomplete.
			if polls <= 2 {
				fmt.Fprintf(w, `{"state":"initializing","finished":false,"progress_bytes":%d,"total_bytes":10000}`, polls*1000)
			} else {
				io.WriteString(w, `{"state":"live","finished":false,"progress_bytes":4000,"total_bytes":10000}`)
			}
		case strings.HasSuffix(r.URL.Path, "/pause"):
			paused = append(paused, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	conn, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	conn.Migrate()
	st := store.New(conn)

	was := quietRecheck
	quietRecheck = 20 * time.Millisecond
	t.Cleanup(func() { quietRecheck = was })

	d := NewDownloader(st, NewPrefetcher(st, nil, torrent.NewClient(srv.URL), discard()), nil, discard())
	d.Quiet(context.Background())

	mu.Lock()
	first := append([]string(nil), paused...)
	mu.Unlock()
	if len(first) != 0 {
		t.Fatalf("paused %v during the check", first)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(paused)
		mu.Unlock()
		if n == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the torrent was never paused once its check ended")
}

func TestProgressReportsCheckingNotPaused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/torrents":
			io.WriteString(w, `{"torrents":[{"id":1,"info_hash":"AAAA","name":"a"},{"id":2,"info_hash":"BBBB","name":"b"}]}`)
		case strings.Contains(r.URL.Path, "/1/stats"):
			io.WriteString(w, `{"state":"initializing","finished":false,"progress_bytes":500,"total_bytes":1000}`)
		case strings.Contains(r.URL.Path, "/2/stats"):
			io.WriteString(w, `{"state":"paused","finished":false,"progress_bytes":500,"total_bytes":1000}`)
		}
	}))
	t.Cleanup(srv.Close)

	c := NewCache(nil, torrent.NewClient(srv.URL), t.TempDir(), discard())
	got, err := c.Progress(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if a := got["aaaa"]; !a.Checking || a.Paused {
		t.Errorf("checking torrent = %+v", a)
	}
	if b := got["bbbb"]; b.Checking || !b.Paused {
		t.Errorf("paused torrent = %+v", b)
	}
}
