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
	"kuro/internal/indexer"
	"kuro/internal/metadata"
	"kuro/internal/score"
	"kuro/internal/store"
	"kuro/internal/torrent"
)

// countingIndexer records every query; the finder fans variants out across
// goroutines, so the record is locked.
type countingIndexer struct {
	mu      sync.Mutex
	queries []string
}

func (c *countingIndexer) Name() string { return "count" }
func (c *countingIndexer) Search(_ context.Context, q indexer.Query) ([]indexer.Torrent, error) {
	c.mu.Lock()
	c.queries = append(c.queries, q.Text)
	c.mu.Unlock()
	return nil, nil
}

func (c *countingIndexer) searched(episode string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, q := range c.queries {
		if strings.HasSuffix(q, " "+episode) {
			return true
		}
	}
	return false
}

func (c *countingIndexer) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.queries)
}

// With the filler rule on, "prepare the next episode" resolves the next one
// worth watching, not simply episode+1.
func TestPrepareFollowsTheFillerRule(t *testing.T) {
	rqbit := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"torrents":[]}`)
	}))
	t.Cleanup(rqbit.Close)
	conn, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	conn.Migrate()
	st := store.New(conn)
	ctx := context.Background()

	mal, eps := 900, 4
	st.ImportList(ctx, []store.Anime{{ID: 1, Romaji: "Show", MalID: &mal, Episodes: &eps}}, nil, store.ImportMerge)
	st.SaveEpisodes(ctx, 1, []metadata.Episode{{Number: 1}, {Number: 2}, {Number: 3}, {Number: 4}})
	st.SaveFillers(ctx, []metadata.Filler{{MalID: 900, Episode: 2, Kind: "filler"}})
	st.SetSetting(ctx, "playback.skip_filler", "true")

	idx := &countingIndexer{}
	finder := NewFinder(st, idx, discard())
	p := NewPrefetcher(st, finder, torrent.NewClient(rqbit.URL), discard())

	p.Prepare(1, 1, 0, score.DefaultPreferences())
	// The resolve runs in the background; wait for it to finish.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		p.mu.Lock()
		busy := len(p.running) > 0
		p.mu.Unlock()
		if !busy && idx.count() > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !idx.searched("03") {
		t.Fatalf("did not search for episode 3: %v", idx.queries)
	}
	if idx.searched("02") {
		t.Fatalf("searched for the filler episode: %v", idx.queries)
	}

	// The last episode has nothing after it, so nothing is searched at all.
	before := idx.count()
	p.Prepare(1, 4, 0, score.DefaultPreferences())
	p.mu.Lock()
	busy := len(p.running)
	p.mu.Unlock()
	if busy != 0 || idx.count() != before {
		t.Fatal("searched past the end of the show")
	}
}
