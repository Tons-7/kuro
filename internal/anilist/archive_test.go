package anilist

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type memArchive struct {
	mu    sync.Mutex
	saved map[string][]byte
	at    time.Time
	wrote chan struct{}
}

func newMemArchive() *memArchive {
	return &memArchive{saved: map[string][]byte{}, at: time.Unix(1_700_000_000, 0), wrote: make(chan struct{}, 8)}
}

func (m *memArchive) LoadAnswer(_ context.Context, key string) ([]byte, time.Time, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.saved[key]
	return b, m.at, ok
}

func (m *memArchive) SaveAnswer(_ context.Context, key string, body []byte) error {
	m.mu.Lock()
	m.saved[key] = body
	m.mu.Unlock()
	m.wrote <- struct{}{}
	return nil
}

// AniList answering, then down: a page gets the last answer and is told so,
// background work gets the error.
func TestSavedAnswerStandsInWhileAniListIsDown(t *testing.T) {
	var down atomic.Bool
	var calls atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		if down.Load() {
			w.Write([]byte(`{"errors":[{"message":"Internal Server Error","status":500}]}`))
			return
		}
		w.Write([]byte(`{"data":{"n":1}}`))
	})
	c.cache = newResponseCache()
	archive := newMemArchive()
	c.SetArchive(archive)

	page, served := WithServed(context.Background())
	var out struct{ N int }
	if err := c.Query(page, "query { n }", nil, &out); err != nil || out.N != 1 {
		t.Fatalf("live query: %v %+v", err, out)
	}
	select {
	case <-archive.wrote:
	case <-time.After(2 * time.Second):
		t.Fatal("a live answer to a page was not saved")
	}
	if live, saved := served.Result(); !live || !saved.IsZero() {
		t.Fatalf("live answer reported as live=%v saved=%v", live, saved)
	}

	down.Store(true)
	c.cache = newResponseCache() // past the ten minutes

	if err := c.Query(context.Background(), "query { n }", nil, &out); err == nil {
		t.Fatal("background work must see the failure, not a saved answer")
	}

	page, served = WithServed(context.Background())
	out.N = 0
	if err := c.Query(page, "query { n }", nil, &out); err != nil || out.N != 1 {
		t.Fatalf("page during outage: %v %+v", err, out)
	}
	if _, saved := served.Result(); !saved.Equal(archive.at) || !UsedSaved(page) {
		t.Fatalf("saved answer not reported: %v", saved)
	}

	// Just failed: the next page read skips AniList instead of waiting on it.
	before := calls.Load()
	page, _ = WithServed(context.Background())
	if err := c.Query(page, "query { n }", nil, &out); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != before {
		t.Error("asked AniList again within the down window")
	}
}

// A rejection is AniList answering; the saved copy must not hide it.
func TestRejectionIsNotReplacedBySavedAnswer(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"errors":[{"message":"Not Found.","status":404}]}`))
	})
	archive := newMemArchive()
	c.SetArchive(archive)
	body := []byte(`{"query":"query { n }","variables":null}`)
	archive.saved[archiveKey(body, false)] = []byte(`{"n":7}`)

	page, _ := WithServed(context.Background())
	var out struct{ N int }
	if err := c.Query(page, "query { n }", nil, &out); err == nil {
		t.Fatalf("a 404 was answered from the archive: %+v", out)
	}
	if UsedSaved(page) {
		t.Error("marked saved without using it")
	}
}
