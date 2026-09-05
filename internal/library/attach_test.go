package library

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"kuro/internal/db"
	"kuro/internal/indexer"
	"kuro/internal/score"
	"kuro/internal/store"
	"kuro/internal/torrent"
)

// fakeRqbit is enough of the engine to drive attach: add a torrent, report its
// state, stream from it, delete it.
type fakeRqbit struct {
	mu      sync.Mutex
	dead    map[string]bool          // by info hash
	slow    map[string]time.Duration // delay before the first byte, by info hash
	ids     map[int]string           // torrent id -> info hash
	added   []string
	deleted []string
	next    int
}

func newFakeRqbit(dead ...string) *fakeRqbit {
	f := &fakeRqbit{dead: map[string]bool{}, slow: map[string]time.Duration{}, ids: map[int]string{}}
	for _, h := range dead {
		f.dead[h] = true
	}
	return f
}

func (f *fakeRqbit) handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /torrents", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		held := make([]map[string]any, 0, len(f.ids))
		for id, hash := range f.ids {
			held = append(held, map[string]any{"id": id, "info_hash": hash, "name": hash})
		}
		f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"torrents": held})
	})

	mux.HandleFunc("POST /torrents", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		hash := hashOf(string(body))

		f.mu.Lock()
		id := 0
		if r.URL.Query().Get("list_only") != "true" {
			f.next++
			id = f.next
			f.ids[id] = hash
			f.added = append(f.added, hash)
		}
		f.mu.Unlock()

		json.NewEncoder(w).Encode(map[string]any{
			"id": id,
			"details": map[string]any{
				"info_hash": hash,
				"name":      "Frieren - 01.mkv",
				"files": []map[string]any{
					{"name": "Frieren - 01.mkv", "components": []string{"Frieren - 01.mkv"},
						"length": 1 << 30, "included": true},
				},
			},
		})
	})

	mux.HandleFunc("GET /torrents/{id}/stats/v1", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"state": "live", "progress_bytes": 0, "total_bytes": 1 << 30, "finished": false,
		})
	})

	// A dead swarm delivers nothing: the connection drops rather than serving
	// bytes, which is how Prewarm learns the release is no good.
	mux.HandleFunc("GET /torrents/{id}/stream/{file}", func(w http.ResponseWriter, r *http.Request) {
		var id int
		fmt.Sscanf(r.PathValue("id"), "%d", &id)

		f.mu.Lock()
		hash := f.ids[id]
		dead := f.dead[hash]
		delay := f.slow[hash]
		f.mu.Unlock()

		if dead {
			if hj, ok := w.(http.Hijacker); ok {
				conn, _, err := hj.Hijack()
				if err == nil {
					conn.Close()
					return
				}
			}
		}
		if delay > 0 {
			time.Sleep(delay)
		}
		w.Write(make([]byte, 4<<20))
	})

	mux.HandleFunc("POST /torrents/{id}/delete", func(w http.ResponseWriter, r *http.Request) {
		var id int
		fmt.Sscanf(r.PathValue("id"), "%d", &id)

		f.mu.Lock()
		f.deleted = append(f.deleted, f.ids[id])
		f.mu.Unlock()
		w.Write([]byte(`{}`))
	})

	return mux
}

func (f *fakeRqbit) wasDeleted(hash string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, h := range f.deleted {
		if h == hash {
			return true
		}
	}
	return false
}

func hashOf(magnet string) string {
	const marker = "urn:btih:"
	i := strings.Index(magnet, marker)
	if i < 0 {
		return ""
	}
	rest := magnet[i+len(marker):]
	if cut := strings.IndexByte(rest, '&'); cut >= 0 {
		rest = rest[:cut]
	}
	return rest
}

type fixedIndexer struct{ results []indexer.Torrent }

func (fixedIndexer) Name() string { return "fixed" }
func (f fixedIndexer) Search(context.Context, indexer.Query) ([]indexer.Torrent, error) {
	return f.results, nil
}

func release(hash, title string, seeders int) indexer.Torrent {
	return indexer.Torrent{
		Title: title, InfoHash: hash, Seeders: seeders, SeedersKnown: true,
		Size: 1 << 30, Category: "1_2", Indexer: "fixed",
	}
}

func newPlayback(t *testing.T, engine *fakeRqbit, results []indexer.Torrent) *Playback {
	t.Helper()

	srv := httptest.NewServer(engine.handler())
	t.Cleanup(srv.Close)

	conn, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := conn.Migrate(); err != nil {
		t.Fatal(err)
	}

	st := store.New(conn)
	episodes := 28
	err = func() error {
		_, err := st.ImportList(context.Background(),
			[]store.Anime{{ID: 1, Romaji: "Sousou no Frieren", Synonyms: "[]", Genres: "[]", Episodes: &episodes}},
			nil, store.ImportMerge)
		return err
	}()
	if err != nil {
		t.Fatal(err)
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	finder := NewFinder(st, fixedIndexer{results: results}, log)
	return NewPlayback(st, finder, torrent.NewClient(srv.URL), nil, t.TempDir(), log)
}

const (
	deadHash  = "1111111111111111111111111111111111111111"
	goodHash  = "2222222222222222222222222222222222222222"
	otherHash = "3333333333333333333333333333333333333333"
)

// A release that never delivers must be removed; rqbit allocates each file at
// full length up front, so an abandoned candidate holds its whole size against
// the cache budget.
func TestAttachRemovesReleasesThatNeverDeliver(t *testing.T) {
	engine := newFakeRqbit(deadHash)
	p := newPlayback(t, engine, []indexer.Torrent{
		release(deadHash, "[Dead] Sousou no Frieren - 01 [1080p].mkv", 900),
		release(goodHash, "[Live] Sousou no Frieren - 01 [1080p].mkv", 50),
	})

	_, _, live, _, _, err := p.attach(context.Background(), PlayRequest{
		AnimeID: 1, Episode: 1, Season: 1, Prefs: score.DefaultPreferences(),
	})
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	if live == nil {
		t.Fatal("no torrent returned")
	}

	if !engine.wasDeleted(deadHash) {
		t.Error("the release that delivered nothing was left downloading")
	}
	if engine.wasDeleted(goodHash) {
		t.Error("the release that worked was deleted")
	}
}

// Deleting is only safe for what this attempt started; a hash the engine already
// held belongs to a finished download.
func TestAttachKeepsReleasesTheEngineAlreadyHeld(t *testing.T) {
	engine := newFakeRqbit(deadHash)
	engine.ids[99] = deadHash

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/torrents" {
			json.NewEncoder(w).Encode(map[string]any{"torrents": []map[string]any{
				{"id": 99, "info_hash": deadHash, "name": "already here"},
			}})
			return
		}
		engine.handler().ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)

	conn, _ := db.Open(filepath.Join(t.TempDir(), "test.db"))
	t.Cleanup(func() { conn.Close() })
	conn.Migrate()
	st := store.New(conn)
	episodes := 28
	st.ImportList(context.Background(),
		[]store.Anime{{ID: 1, Romaji: "Sousou no Frieren", Synonyms: "[]", Genres: "[]", Episodes: &episodes}},
		nil, store.ImportMerge)

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	finder := NewFinder(st, fixedIndexer{results: []indexer.Torrent{
		release(deadHash, "[Dead] Sousou no Frieren - 01 [1080p].mkv", 900),
		release(goodHash, "[Live] Sousou no Frieren - 01 [1080p].mkv", 50),
	}}, log)
	p := NewPlayback(st, finder, torrent.NewClient(srv.URL), nil, t.TempDir(), log)

	if _, _, _, _, _, err := p.attach(context.Background(), PlayRequest{
		AnimeID: 1, Episode: 1, Season: 1, Prefs: score.DefaultPreferences(),
	}); err != nil {
		t.Fatalf("attach: %v", err)
	}

	if engine.wasDeleted(deadHash) {
		t.Error("deleted a download the engine already held")
	}
}

// The best release wins whenever it can deliver, even if a lower-ranked one
// answers first; racing for speed must never trade quality for a head start.
func TestAttachPrefersBetterReleaseEvenWhenSlower(t *testing.T) {
	old := raceStagger
	raceStagger = 10 * time.Millisecond
	t.Cleanup(func() { raceStagger = old })

	engine := newFakeRqbit()
	// The better release, ranked first, delivers only after the worse one has.
	engine.slow[goodHash] = 400 * time.Millisecond

	p := newPlayback(t, engine, []indexer.Torrent{
		release(goodHash, "[Best] Sousou no Frieren - 01 [1080p BluRay].mkv", 900),
		release(otherHash, "[Worse] Sousou no Frieren - 01 [720p].mkv", 50),
	})

	rel, _, live, _, _, err := p.attach(context.Background(), PlayRequest{
		AnimeID: 1, Episode: 1, Season: 1, Prefs: score.DefaultPreferences(),
	})
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	if live == nil {
		t.Fatal("no torrent returned")
	}
	if rel.Torrent.InfoHash != goodHash {
		t.Fatalf("took the faster-but-worse release %s, want the better %s", rel.Torrent.InfoHash, goodHash)
	}

	// The worse release, warmed in parallel, is dropped once the better one wins.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !engine.wasDeleted(otherHash) {
		time.Sleep(20 * time.Millisecond)
	}
	if !engine.wasDeleted(otherHash) {
		t.Error("the worse release warmed in the race was not discarded")
	}
	if engine.wasDeleted(goodHash) {
		t.Error("the chosen release was discarded")
	}
}

// Resolving a release for a series-page open must add nothing to the engine: it
// is cached in memory and the download starts only when play does, so browsing
// never fills the Downloads tab.
func TestPrepareResolvesWithoutTouchingTheEngine(t *testing.T) {
	engine := newFakeRqbit()
	srv := httptest.NewServer(engine.handler())
	t.Cleanup(srv.Close)

	conn, _ := db.Open(filepath.Join(t.TempDir(), "test.db"))
	t.Cleanup(func() { conn.Close() })
	conn.Migrate()
	st := store.New(conn)
	episodes := 28
	st.ImportList(context.Background(),
		[]store.Anime{{ID: 1, Romaji: "Sousou no Frieren", Synonyms: "[]", Genres: "[]", Episodes: &episodes}},
		nil, store.ImportMerge)

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	finder := NewFinder(st, fixedIndexer{results: []indexer.Torrent{
		release(goodHash, "[Best] Sousou no Frieren - 01 [1080p BluRay].mkv", 900),
	}}, log)
	p := NewPrefetcher(st, finder, torrent.NewClient(srv.URL), log)

	if err := p.prepare(context.Background(), 1, 1, 1, score.DefaultPreferences()); err != nil {
		t.Fatalf("prepare: %v", err)
	}

	engine.mu.Lock()
	added := append([]string(nil), engine.added...)
	engine.mu.Unlock()
	if len(added) != 0 {
		t.Errorf("prepare added torrents to the engine: %v", added)
	}

	rel, ok := p.TakePrepared(1, 1)
	if !ok {
		t.Fatal("the resolved release was not cached for play to use")
	}
	if rel.Torrent.InfoHash != goodHash {
		t.Errorf("cached the wrong release: %s", rel.Torrent.InfoHash)
	}
	if _, again := p.TakePrepared(1, 1); again {
		t.Error("the cached release was not consumed")
	}
}

// Play uses the release resolved ahead of time and does not search again: here
// the finder has nothing, so a fresh search would fail — success proves the
// prepared release was used.
func TestAttachUsesThePreparedRelease(t *testing.T) {
	engine := newFakeRqbit()
	p := newPlayback(t, engine, nil)

	pref := NewPrefetcher(nil, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	pref.prepared[prepareKey(1, 1)] = preparedRelease{
		result: score.Result{Candidate: score.Candidate{Torrent: release(goodHash, "[Best] Sousou no Frieren - 01 [1080p].mkv", 900)}},
		at:     time.Now(),
	}
	p.WithPrefetcher(pref)

	rel, _, live, _, _, err := p.attach(context.Background(), PlayRequest{
		AnimeID: 1, Episode: 1, Season: 1, Prefs: score.DefaultPreferences(),
	})
	if err != nil {
		t.Fatalf("attach did not use the prepared release (fell through to an empty search): %v", err)
	}
	if live == nil || rel.Torrent.InfoHash != goodHash {
		t.Fatalf("attached the wrong release: %+v", rel.Torrent.InfoHash)
	}
	if _, ok := pref.TakePrepared(1, 1); ok {
		t.Error("the prepared release was not consumed by attach")
	}
}
