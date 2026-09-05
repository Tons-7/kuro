package anilist

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func testClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	return &Client{
		http:     srv.Client(),
		limiter:  rate.NewLimiter(rate.Inf, 1),
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		endpoint: srv.URL,
	}
}

// AniList derives the HTTP status from its error array and can return a
// non-200 alongside usable data, so errors must be read from the body.
func TestQueryReadsErrorsRegardlessOfStatus(t *testing.T) {
	for _, status := range []int{200, 400, 404} {
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			io.WriteString(w, `{"data":null,"errors":[{"message":"boom","status":400}]}`)
		})

		err := c.Query(context.Background(), "{x}", nil, nil)
		if err == nil {
			t.Fatalf("status %d: expected an error", status)
		}
		if _, ok := err.(Errors); !ok {
			t.Fatalf("status %d: want Errors, got %T", status, err)
		}
	}
}

func TestQueryDecodesData(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("content-type = %q", got)
		}
		io.WriteString(w, `{"data":{"n":42}}`)
	})

	var out struct {
		N int `json:"n"`
	}
	if err := c.Query(context.Background(), "{n}", nil, &out); err != nil {
		t.Fatal(err)
	}
	if out.N != 42 {
		t.Fatalf("n = %d, want 42", out.N)
	}
}

func TestQueryRetriesAfter429(t *testing.T) {
	var calls int
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		io.WriteString(w, `{"data":{"ok":true}}`)
	})

	if err := c.Query(context.Background(), "{ok}", nil, nil); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

func TestQuerySendsBearerOnlyWhenSet(t *testing.T) {
	var seen string
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("Authorization")
		io.WriteString(w, `{"data":{}}`)
	})

	c.Query(context.Background(), "{}", nil, nil)
	if seen != "" {
		t.Fatalf("unauthenticated request sent %q", seen)
	}

	c.SetToken("abc")
	c.Query(context.Background(), "{}", nil, nil)
	if seen != "Bearer abc" {
		t.Fatalf("authorization = %q", seen)
	}
}

// The burst limiter returns 429s carrying no rate-limit headers at all, so the
// fallback must not depend on any of them being present.
func TestRetryAfterFallbacks(t *testing.T) {
	reset := time.Now().Add(30 * time.Second).Unix()

	tests := []struct {
		name    string
		headers map[string]string
		min     time.Duration
		max     time.Duration
	}{
		{"retry-after wins", map[string]string{"Retry-After": "5"}, 5 * time.Second, 6 * time.Second},
		{"falls back to reset", map[string]string{"X-RateLimit-Reset": strconv.FormatInt(reset, 10)}, 25 * time.Second, 31 * time.Second},
		{"no headers at all", nil, time.Minute, time.Minute},
		{"unparseable retry-after", map[string]string{"Retry-After": "soon"}, time.Minute, time.Minute},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := &http.Response{Header: http.Header{}}
			for k, v := range tt.headers {
				res.Header.Set(k, v)
			}
			got := retryAfter(res)
			if got < tt.min || got > tt.max {
				t.Fatalf("got %v, want between %v and %v", got, tt.min, tt.max)
			}
		})
	}
}

func TestErrorsUnauthorized(t *testing.T) {
	tests := []struct {
		name string
		errs Errors
		want bool
	}{
		{"401", Errors{{Status: 401, Message: "Unauthorized."}}, true},
		{"400 invalid token", Errors{{Status: 400, Message: "Invalid token"}}, true},
		{"403 api disabled", Errors{{Status: 403, Message: "temporarily disabled"}}, false},
		{"400 bad query", Errors{{Status: 400, Message: `Cannot query field "x"`}}, false},
		{"empty", Errors{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.errs.Unauthorized(); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRemainingMissingHeader(t *testing.T) {
	res := &http.Response{Header: http.Header{}}
	if got := remaining(res); got != -1 {
		t.Fatalf("got %d, want -1 for an absent header", got)
	}
}

// An exhausted quota must not stall the response that observed it; the caller
// already has its data.
func TestExhaustedQuotaReturnsImmediately(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		io.WriteString(w, `{"data":{"n":1}}`)
	})

	start := time.Now()
	if err := c.Query(context.Background(), "{n}", nil, nil); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("returned after %v; the pause belongs on the next request", elapsed)
	}

	c.mu.Lock()
	paused := time.Until(c.pausedUntil)
	c.mu.Unlock()
	if paused <= 0 {
		t.Fatal("no pause was recorded for subsequent requests")
	}
}

// A 429 seen by one goroutine has to hold back all of them, since the quota
// belongs to the account rather than the connection.
func TestPauseAppliesAcrossCallers(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"data":{}}`)
	})
	c.pauseFor(300 * time.Millisecond)

	start := time.Now()
	if err := c.Query(context.Background(), "{}", nil, nil); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed < 250*time.Millisecond {
		t.Fatalf("request went out after %v, ignoring the active pause", elapsed)
	}
}

func TestPauseNeverShortensAnExistingOne(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {})

	c.pauseFor(time.Hour)
	c.pauseFor(time.Second)

	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Until(c.pausedUntil) < 30*time.Minute {
		t.Fatal("a shorter pause overwrote a longer one")
	}
}

func TestAwaitPauseRespectsContext(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {})
	c.pauseFor(time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := c.awaitPause(ctx); err == nil {
		t.Fatal("a cancelled context should abort the wait")
	}
}
