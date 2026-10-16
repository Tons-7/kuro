package mal

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// raceClient's token endpoint holds a refresh until release is closed; a login answers at once.
func raceClient(t *testing.T) (c *Client, started, release chan struct{}, stored func() Token) {
	t.Helper()
	started, release = make(chan struct{}), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/token" {
			io.WriteString(w, `{"id":1,"name":"tony"}`)
			return
		}
		r.ParseForm()
		if r.PostForm.Get("grant_type") == "refresh_token" {
			close(started)
			<-release
			io.WriteString(w, `{"token_type":"Bearer","expires_in":3600,"access_token":"old-fresh","refresh_token":"old-ref-2"}`)
			return
		}
		io.WriteString(w, `{"token_type":"Bearer","expires_in":3600,"access_token":"new-acc","refresh_token":"new-ref"}`)
	}))
	t.Cleanup(srv.Close)

	var mu sync.Mutex
	var saved Token
	c = New(slog.New(slog.NewTextHandler(io.Discard, nil)), WithAPI(srv.URL), WithOAuthBase(srv.URL),
		WithCredentials("id", ""), WithoutRateLimit(), OnToken(func(t Token) { mu.Lock(); saved = t; mu.Unlock() }))
	c.SetToken(Token{Access: "old", Refresh: "old-ref", Expires: time.Now().Add(time.Minute)})
	return c, started, release, func() Token { mu.Lock(); defer mu.Unlock(); return saved }
}

// Disconnecting while a refresh is in flight must not end with the refreshed token stored again.
func TestLogoutDuringRefreshStaysLoggedOut(t *testing.T) {
	c, started, release, stored := raceClient(t)
	done := make(chan struct{})
	go func() { c.Viewer(t.Context()); close(done) }()
	<-started

	var mu sync.Mutex
	cleared := false
	forgot := make(chan error)
	go func() {
		forgot <- c.Forget(func() error { mu.Lock(); cleared = true; mu.Unlock(); return nil })
	}()
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	early := cleared
	mu.Unlock()
	if early {
		t.Fatal("logout cleared the settings while a refresh could still write them back")
	}

	close(release)
	if err := <-forgot; err != nil {
		t.Fatal(err)
	}
	<-done
	if c.Token().Access != "" || c.Authenticated() {
		t.Fatalf("token after logout = %+v", c.Token())
	}
	if got := stored(); got.Access != "old-fresh" {
		t.Fatalf("stored = %+v; the refresh should have finished before the clear ran", got)
	}
}

// A new login while the old account's refresh is in flight keeps the new account.
func TestLoginDuringRefreshKeepsTheNewAccount(t *testing.T) {
	c, started, release, stored := raceClient(t)
	done := make(chan error)
	go func() { _, err := c.Viewer(t.Context()); done <- err }()
	<-started

	if _, err := c.Exchange(t.Context(), "code", "verifier", ""); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("the request in flight failed: %v", err)
	}
	if got := c.Token(); got.Access != "new-acc" || got.Refresh != "new-ref" {
		t.Fatalf("token = %+v, want the new login's", got)
	}
	if got := stored(); got.Access != "new-acc" {
		t.Fatalf("stored = %+v, want the new login's", got)
	}
}
