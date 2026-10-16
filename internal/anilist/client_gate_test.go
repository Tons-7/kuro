package anilist

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A rate-limit pause holds the anonymous fallback too, rather than letting it fire into the same 429.
func TestAnonymousQueryHonoursThePause(t *testing.T) {
	var hits atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		io.WriteString(w, `{"data":{}}`)
	})
	c.cache = newResponseCache()
	c.SetToken("tok")
	c.pauseFor(time.Hour)

	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
	defer cancel()
	if err := c.queryAnonymous(ctx, `query { Page { media { id } } }`, nil, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want it held by the pause", err)
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("%d requests went out during the pause", n)
	}
}

// The anonymous request carries no token, and its answer is cached apart from the account's.
func TestAnonymousQueryWithholdsTheToken(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			io.WriteString(w, `{"data":{"who":"anon"}}`)
			return
		}
		io.WriteString(w, `{"data":{"who":"user"}}`)
	})
	c.cache = newResponseCache()
	c.SetToken("tok")

	var out struct{ Who string }
	const q = `query { who }`
	if err := c.queryAnonymous(t.Context(), q, nil, &out); err != nil || out.Who != "anon" {
		t.Fatalf("anonymous = %q, %v", out.Who, err)
	}
	if err := c.Query(t.Context(), q, nil, &out); err != nil || out.Who != "user" {
		t.Fatalf("with the token = %q, %v; the anonymous answer leaked into its cache", out.Who, err)
	}
}

// A caller waiting on someone else's identical request gives up when its own context ends.
func TestWaiterGivesUpWithItsContext(t *testing.T) {
	release := make(chan struct{})
	asked := make(chan struct{}, 1)
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		asked <- struct{}{}
		<-release
		io.WriteString(w, `{"data":{}}`)
	})
	c.cache = newResponseCache()
	defer close(release)

	const q = `query { slow }`
	go c.Query(context.Background(), q, nil, nil)
	<-asked

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := c.Query(ctx, q, nil, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	if waited := time.Since(start); waited > time.Second {
		t.Fatalf("waited %v for a request it no longer wanted", waited)
	}
}

// A failed exchange says what the server said, JSON or not.
func TestExchangeErrorsKeepTheResponse(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{`{"error":"invalid_client","error_description":"Client authentication failed","hint":"check the secret"}`,
			"invalid_client: Client authentication failed: check the secret"},
		{"<html><body>\n  <h1>Access denied</h1> by the WAF</body></html>", "<h1>Access denied</h1> by the WAF"},
		{"", "(empty body)"},
	} {
		c := oauthClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			io.WriteString(w, tc.body)
		})
		_, err := c.Exchange(t.Context(), "id", "secret", "http://x/cb", "code")
		if err == nil || !strings.Contains(err.Error(), "HTTP 403") || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("err = %v, want %q in it", err, tc.want)
		}
	}
}
