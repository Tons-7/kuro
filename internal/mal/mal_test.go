package mal

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newClient(t *testing.T, h http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	c := New(slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithAPI(srv.URL), WithOAuthBase(srv.URL),
		WithCredentials("client-123", "secret-456"),
		WithoutRateLimit(),
		withBackoff(func(int) time.Duration { return time.Millisecond }))
	return c, srv
}

// A transient 5xx should not surface; MAL returns them under load.
func TestTransientServerErrorIsRetried(t *testing.T) {
	var attempts atomic.Int32
	c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		io.WriteString(w, `{"id":1,"name":"tony"}`)
	})
	c.SetToken(Token{Access: "acc", Expires: time.Now().Add(time.Hour)})

	v, err := c.Viewer(t.Context())
	if err != nil {
		t.Fatalf("gave up after %d attempts: %v", attempts.Load(), err)
	}
	if v.Name != "tony" {
		t.Errorf("viewer = %+v", v)
	}
	if attempts.Load() != 3 {
		t.Errorf("%d attempts, want 3", attempts.Load())
	}
}

// Retries are bounded; a permanently broken upstream must surface.
func TestRetriesAreBounded(t *testing.T) {
	var attempts atomic.Int32
	c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	})
	c.SetToken(Token{Access: "acc", Expires: time.Now().Add(time.Hour)})

	if _, err := c.Viewer(t.Context()); err == nil {
		t.Fatal("expected an error")
	}
	if got := attempts.Load(); got != maxRetries+1 {
		t.Errorf("%d attempts, want %d", got, maxRetries+1)
	}
}

func TestVerifierMeetsPKCELength(t *testing.T) {
	seen := map[string]bool{}
	for range 20 {
		v := Verifier()
		if len(v) < 43 || len(v) > 128 {
			t.Fatalf("verifier length %d outside the PKCE range", len(v))
		}
		if strings.ContainsAny(v, "+/=") {
			t.Fatalf("verifier %q is not URL-safe", v)
		}
		if seen[v] {
			t.Fatal("verifier repeated")
		}
		seen[v] = true
	}
}

// MAL supports only the plain challenge method, so the challenge is the
// verifier verbatim. Sending S256 would make every exchange fail.
func TestAuthURLUsesPlainChallenge(t *testing.T) {
	c, srv := newClient(t, nil)
	raw := c.AuthURL("verifier-value", "state-value", "http://localhost:4321/mal/callback")

	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(raw, srv.URL+"/authorize") {
		t.Fatalf("authorize URL = %s", raw)
	}

	q := u.Query()
	want := map[string]string{
		"response_type":         "code",
		"client_id":             "client-123",
		"code_challenge":        "verifier-value",
		"code_challenge_method": "plain",
		"state":                 "state-value",
		"redirect_uri":          "http://localhost:4321/mal/callback",
	}
	for k, v := range want {
		if got := q.Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
}

func TestExchangeStoresTokenAndNotifies(t *testing.T) {
	var form url.Values
	c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		form = r.PostForm
		io.WriteString(w, `{"token_type":"Bearer","expires_in":3600,
		  "access_token":"acc-1","refresh_token":"ref-1"}`)
	})

	var saved Token
	c.onToken = func(tk Token) { saved = tk }

	tok, err := c.Exchange(t.Context(), "the-code", "the-verifier", "http://localhost:4321/cb")
	if err != nil {
		t.Fatal(err)
	}

	if form.Get("grant_type") != "authorization_code" {
		t.Errorf("grant_type = %q", form.Get("grant_type"))
	}
	if form.Get("code_verifier") != "the-verifier" {
		t.Errorf("code_verifier = %q", form.Get("code_verifier"))
	}
	if form.Get("client_secret") != "secret-456" {
		t.Errorf("client_secret missing")
	}
	if tok.Access != "acc-1" || tok.Refresh != "ref-1" {
		t.Fatalf("token = %+v", tok)
	}
	if d := time.Until(tok.Expires); d < 50*time.Minute || d > time.Hour+time.Minute {
		t.Errorf("expiry in %s, want about an hour", d)
	}
	if saved.Access != "acc-1" {
		t.Error("onToken was not called with the new token")
	}
	if !c.Authenticated() {
		t.Error("client did not adopt the token")
	}
}

// MAL may omit refresh_token on a refresh; the old one stays valid and losing
// it would silently end the connection an hour later.
func TestRefreshKeepsExistingRefreshToken(t *testing.T) {
	c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"token_type":"Bearer","expires_in":3600,"access_token":"acc-2"}`)
	})
	c.SetToken(Token{Access: "acc-1", Refresh: "ref-1", Expires: time.Now().Add(time.Minute)})

	tok, err := c.Refresh(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if tok.Access != "acc-2" {
		t.Errorf("access = %q", tok.Access)
	}
	if tok.Refresh != "ref-1" {
		t.Errorf("refresh = %q, want the previous one retained", tok.Refresh)
	}
}

func TestRefreshWithoutRefreshTokenFails(t *testing.T) {
	c, _ := newClient(t, nil)
	c.SetToken(Token{Access: "acc-1"})

	if _, err := c.Refresh(t.Context()); err == nil {
		t.Fatal("expected an error")
	}
}

// Tokens last an hour, so a long-running install has to renew mid-request.
func TestExpiringTokenIsRefreshedBeforeUse(t *testing.T) {
	var refreshes, calls atomic.Int32
	c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/token") {
			refreshes.Add(1)
			io.WriteString(w, `{"token_type":"Bearer","expires_in":3600,
			  "access_token":"fresh","refresh_token":"ref-2"}`)
			return
		}
		calls.Add(1)
		if got := r.Header.Get("Authorization"); got != "Bearer fresh" {
			t.Errorf("request used %q, want the refreshed token", got)
		}
		io.WriteString(w, `{"id":1,"name":"tony"}`)
	})

	c.SetToken(Token{Access: "stale", Refresh: "ref-1", Expires: time.Now().Add(time.Minute)})
	if _, err := c.Viewer(t.Context()); err != nil {
		t.Fatal(err)
	}

	if refreshes.Load() != 1 {
		t.Fatalf("%d refreshes, want 1", refreshes.Load())
	}
	if calls.Load() != 1 {
		t.Fatalf("%d api calls, want 1", calls.Load())
	}
}

func TestHealthyTokenIsNotRefreshed(t *testing.T) {
	var refreshes atomic.Int32
	c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/token") {
			refreshes.Add(1)
		}
		io.WriteString(w, `{"id":1,"name":"tony"}`)
	})

	c.SetToken(Token{Access: "good", Refresh: "ref-1", Expires: time.Now().Add(time.Hour)})
	if _, err := c.Viewer(t.Context()); err != nil {
		t.Fatal(err)
	}
	if refreshes.Load() != 0 {
		t.Fatalf("refreshed %d times with an hour left", refreshes.Load())
	}
}

func TestListFollowsPaging(t *testing.T) {
	var srv *httptest.Server
	var pages atomic.Int32

	c, s := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		n := pages.Add(1)
		if n == 1 {
			fmt.Fprintf(w, `{"data":[
			  {"node":{"id":52991,"title":"Sousou no Frieren","num_episodes":28},
			   "list_status":{"status":"watching","score":9,"num_episodes_watched":10,
			                  "updated_at":"2026-08-01T12:00:00+00:00"}}],
			  "paging":{"next":"%s/users/@me/animelist?offset=1000"}}`, srv.URL)
			return
		}
		io.WriteString(w, `{"data":[
		  {"node":{"id":16498,"title":"Shingeki no Kyojin","num_episodes":25},
		   "list_status":{"status":"completed","score":10,"num_episodes_watched":25,
		                  "updated_at":"2026-07-01T12:00:00+00:00"}}],
		  "paging":{}}`)
	})
	srv = s
	c.SetToken(Token{Access: "acc", Expires: time.Now().Add(time.Hour)})

	entries, err := c.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries across %d pages", len(entries), pages.Load())
	}

	first := entries[0]
	if first.AnimeID != 52991 || first.Watched != 10 || first.Episodes != 28 {
		t.Errorf("first entry = %+v", first)
	}
	if first.Status != StatusWatching {
		t.Errorf("status = %q", first.Status)
	}
	if first.Updated.IsZero() {
		t.Error("updated_at not parsed")
	}
	if entries[1].AnimeID != 16498 || entries[1].Watched != 25 {
		t.Errorf("second entry = %+v", entries[1])
	}
}

// Reads return num_episodes_watched, writes take num_watched_episodes; the read
// spelling returns 200 and changes nothing.
func TestSetProgressUsesTheWriteSpelling(t *testing.T) {
	var form url.Values
	var method, path string

	c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		form, method, path = r.PostForm, r.Method, r.URL.Path
		io.WriteString(w, `{"status":"watching","num_episodes_watched":11}`)
	})
	c.SetToken(Token{Access: "acc", Expires: time.Now().Add(time.Hour)})

	if err := c.SetProgress(t.Context(), 52991, 11, "CURRENT", 0, 0); err != nil {
		t.Fatal(err)
	}

	if method != "PATCH" {
		t.Errorf("method = %s", method)
	}
	if path != "/anime/52991/my_list_status" {
		t.Errorf("path = %s", path)
	}
	if got := form.Get("num_watched_episodes"); got != "11" {
		t.Errorf("num_watched_episodes = %q, want 11", got)
	}
	if form.Has("num_episodes_watched") {
		t.Error("sent the read-side spelling, which MAL silently ignores")
	}
	if got := form.Get("status"); got != StatusWatching {
		t.Errorf("status = %q, want %q", got, StatusWatching)
	}
}

func TestSetProgressOmitsUnknownStatus(t *testing.T) {
	var form url.Values
	c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		form = r.PostForm
		io.WriteString(w, `{}`)
	})
	c.SetToken(Token{Access: "acc", Expires: time.Now().Add(time.Hour)})

	if err := c.SetProgress(t.Context(), 1, 3, "SOMETHING_ELSE", 0, 0); err != nil {
		t.Fatal(err)
	}
	if form.Has("status") {
		t.Errorf("status = %q, want it left alone", form.Get("status"))
	}
}

func TestSetProgressRejectsBadID(t *testing.T) {
	c, _ := newClient(t, nil)
	c.SetToken(Token{Access: "acc", Expires: time.Now().Add(time.Hour)})

	if err := c.SetProgress(t.Context(), 0, 1, "CURRENT", 0, 0); err == nil {
		t.Fatal("expected an error for anime id 0")
	}
}

// MAL has no rewatching status: a rewatch is "watching" with is_rewatching,
// and finished rewatches are a count, both sent with every update.
func TestSetProgressSendsRewatchFields(t *testing.T) {
	var form url.Values
	c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		form = r.PostForm
		io.WriteString(w, `{}`)
	})
	c.SetToken(Token{Access: "acc", Expires: time.Now().Add(time.Hour)})

	if err := c.SetProgress(t.Context(), 5, 4, "REPEATING", 2, 0); err != nil {
		t.Fatal(err)
	}
	if form.Get("status") != StatusWatching || form.Get("is_rewatching") != "true" {
		t.Errorf("rewatch sent as status=%q is_rewatching=%q", form.Get("status"), form.Get("is_rewatching"))
	}
	if form.Get("num_times_rewatched") != "2" {
		t.Errorf("num_times_rewatched = %q, want 2", form.Get("num_times_rewatched"))
	}

	if err := c.SetProgress(t.Context(), 5, 12, "COMPLETED", 3, 0); err != nil {
		t.Fatal(err)
	}
	if form.Get("is_rewatching") != "false" || form.Get("num_times_rewatched") != "3" {
		t.Errorf("finishing: is_rewatching=%q num_times_rewatched=%q", form.Get("is_rewatching"), form.Get("num_times_rewatched"))
	}
}

func TestStatusMapping(t *testing.T) {
	cases := map[string]string{
		"CURRENT":   StatusWatching,
		"REPEATING": StatusWatching,
		"COMPLETED": StatusCompleted,
		"PAUSED":    StatusOnHold,
		"DROPPED":   StatusDropped,
		"PLANNING":  StatusPlanToWatch,
		"current":   StatusWatching,
		"":          "",
		"NONSENSE":  "",
	}
	for in, want := range cases {
		if got := Status(in); got != want {
			t.Errorf("Status(%q) = %q, want %q", in, got, want)
		}
	}

	// Rewatching collapses into watching, so it cannot round-trip on the
	// status alone; the is_rewatching flag carries it back.
	for _, s := range []string{"CURRENT", "COMPLETED", "PAUSED", "DROPPED", "PLANNING"} {
		if got := StatusToAniList(Status(s)); got != s {
			t.Errorf("round trip of %s produced %s", s, got)
		}
	}
	if got := (Entry{Status: StatusWatching, Rewatching: true}).ListStatus(); got != "REPEATING" {
		t.Errorf("watching + is_rewatching = %q, want REPEATING", got)
	}
	if got := (Entry{Status: StatusWatching}).ListStatus(); got != "CURRENT" {
		t.Errorf("plain watching = %q, want CURRENT", got)
	}
	// A stale flag on a finished entry does not make it a rewatch in progress.
	if got := (Entry{Status: StatusCompleted, Rewatching: true}).ListStatus(); got != "COMPLETED" {
		t.Errorf("completed + is_rewatching = %q, want COMPLETED", got)
	}
}

func TestUnauthorizedClassification(t *testing.T) {
	c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":"invalid_token","message":"token expired"}`)
	})
	c.SetToken(Token{Access: "acc", Expires: time.Now().Add(time.Hour)})

	_, err := c.Viewer(t.Context())
	if err == nil {
		t.Fatal("expected an error")
	}
	if !Unauthorized(err) {
		t.Errorf("%v not classified as needing reconnection", err)
	}
	if !strings.Contains(err.Error(), "token expired") {
		t.Errorf("error text lost the message: %v", err)
	}
}

// An expired refresh token is a 400, not a 401, and still means reconnect.
func TestInvalidGrantIsUnauthorized(t *testing.T) {
	c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"error":"invalid_grant","message":"refresh token expired"}`)
	})
	c.SetToken(Token{Access: "acc", Refresh: "ref-old"})

	_, err := c.Refresh(t.Context())
	if err == nil {
		t.Fatal("expected an error")
	}
	if !Unauthorized(err) {
		t.Errorf("%v should require reconnection", err)
	}
}

func TestServerErrorIsNotMistakenForAuth(t *testing.T) {
	c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		io.WriteString(w, `<html>gateway down</html>`)
	})
	c.SetToken(Token{Access: "acc", Expires: time.Now().Add(time.Hour)})

	_, err := c.Viewer(t.Context())
	if err == nil {
		t.Fatal("expected an error")
	}
	if Unauthorized(err) {
		t.Error("a 502 must not force the user to reconnect")
	}
	if !strings.Contains(err.Error(), "502") {
		t.Errorf("status lost: %v", err)
	}
}

func TestConfiguredReportsMissingCredentials(t *testing.T) {
	bare := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if bare.Configured() {
		t.Error("a client with no client_id is not configured")
	}
	if _, err := bare.Exchange(context.Background(), "code", "verifier", ""); err == nil {
		t.Error("exchange should refuse without a client_id")
	}
}
