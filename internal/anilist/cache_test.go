package anilist

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func cachingServer(t *testing.T, hits *atomic.Int64) *Client {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "mutation") {
			w.Write([]byte(`{"data":{"SaveMediaListEntry":{"id":1}}}`))
			return
		}
		w.Write([]byte(`{"data":{"Page":{"media":[{"id":1}]}}}`))
	}))
	t.Cleanup(srv.Close)

	return New(slog.New(slog.DiscardHandler), WithEndpoint(srv.URL), WithoutRateLimit())
}

// A week of schedule is nine requests out of a budget of thirty a minute, and
// every page showing what airs tonight asks for it.
func TestRepeatedQueryHitsTheNetworkOnce(t *testing.T) {
	var hits atomic.Int64
	c := cachingServer(t, &hits)
	ctx := context.Background()

	for range 5 {
		var out struct{}
		if err := c.Query(ctx, `query Q { Page { media { id } } }`, nil, &out); err != nil {
			t.Fatal(err)
		}
	}
	if hits.Load() != 1 {
		t.Errorf("%d requests, want 1", hits.Load())
	}

	// Different variables are a different question.
	var out struct{}
	if err := c.Query(ctx, `query Q { Page { media { id } } }`,
		map[string]any{"page": 2}, &out); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 2 {
		t.Errorf("%d requests after a new query, want 2", hits.Load())
	}
}

func TestMutationIsNeverCached(t *testing.T) {
	var hits atomic.Int64
	c := cachingServer(t, &hits)
	ctx := context.Background()

	for range 3 {
		var out struct{}
		if err := c.Query(ctx, `mutation Save { SaveMediaListEntry { id } }`, nil, &out); err != nil {
			t.Fatal(err)
		}
	}
	if hits.Load() != 3 {
		t.Errorf("%d requests, want 3: a mutation must always be sent", hits.Load())
	}
}

// Opening the app fires several identical queries at once. Without this they
// all miss the empty cache and each spend a request.
func TestConcurrentIdenticalQueriesShareOneRequest(t *testing.T) {
	var hits atomic.Int64
	c := cachingServer(t, &hits)

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var out struct{}
			if err := c.Query(context.Background(),
				`query Q { Page { media { id } } }`, nil, &out); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()

	if hits.Load() != 1 {
		t.Errorf("%d requests, want 1", hits.Load())
	}
}

// The same document returns different results with and without a token, since
// AniList hides adult titles from an account that has not opted into them.
func TestTokenIsPartOfTheCacheKey(t *testing.T) {
	var hits atomic.Int64
	c := cachingServer(t, &hits)
	ctx := context.Background()

	var out struct{}
	if err := c.Query(ctx, `query Q { Page { media { id } } }`, nil, &out); err != nil {
		t.Fatal(err)
	}
	c.SetToken("secret")
	if err := c.Query(ctx, `query Q { Page { media { id } } }`, nil, &out); err != nil {
		t.Fatal(err)
	}

	if hits.Load() != 2 {
		t.Errorf("%d requests, want 2: an anonymous answer was reused for an account", hits.Load())
	}
}
