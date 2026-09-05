package anilist

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func oauthClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c.oauthBase = srv.URL
	return c
}

func TestAuthorizeURL(t *testing.T) {
	c := New(nil)
	raw := c.AuthorizeURL("1234", "http://localhost:4321/callback", "abc123")

	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(raw, OAuthBase+"/authorize?") {
		t.Fatalf("unexpected base: %s", raw)
	}

	want := map[string]string{
		"client_id":     "1234",
		"redirect_uri":  "http://localhost:4321/callback",
		"response_type": "code",
		"state":         "abc123",
	}
	for k, v := range want {
		if got := u.Query().Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
}

func TestExchangeSendsAuthorizationCodeGrant(t *testing.T) {
	var body map[string]string
	c := oauthClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/token" {
			t.Errorf("path = %s", r.URL.Path)
		}
		json.NewDecoder(r.Body).Decode(&body)
		io.WriteString(w, `{"token_type":"Bearer","expires_in":31536000,"access_token":"jwt-here"}`)
	})

	token, err := c.Exchange(context.Background(), "1234", "secret", "http://localhost:4321/callback", "the-code")
	if err != nil {
		t.Fatal(err)
	}
	if token.AccessToken != "jwt-here" {
		t.Fatalf("access token = %q", token.AccessToken)
	}

	want := map[string]string{
		"grant_type":    "authorization_code",
		"client_id":     "1234",
		"client_secret": "secret",
		"code":          "the-code",
		"redirect_uri":  "http://localhost:4321/callback",
	}
	for k, v := range want {
		if body[k] != v {
			t.Errorf("%s = %q, want %q", k, body[k], v)
		}
	}

	if d := time.Until(token.ExpiresAt()); d < 360*24*time.Hour {
		t.Fatalf("expiry is %v away, expected roughly a year", d)
	}
}

func TestExchangeFailures(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{"rejected client", http.StatusUnauthorized, `{"error":"invalid_client","message":"Client authentication failed"}`},
		{"server error", http.StatusInternalServerError, `{}`},
		{"empty token", http.StatusOK, `{"token_type":"Bearer"}`},
		{"malformed json", http.StatusOK, `not json`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := oauthClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				io.WriteString(w, tt.body)
			})
			if _, err := c.Exchange(context.Background(), "1", "s", "r", "c"); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestViewer(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"data":{"Viewer":{"id":42,"name":"tony","mediaListOptions":{"scoreFormat":"POINT_100"}}}}`)
	})

	v, err := c.Viewer(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v.ID != 42 || v.Name != "tony" {
		t.Fatalf("viewer = %+v", v)
	}
	if v.MediaListOptions.ScoreFormat != "POINT_100" {
		t.Fatalf("score format = %q", v.MediaListOptions.ScoreFormat)
	}
}

func TestSeason(t *testing.T) {
	var vars map[string]any
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Variables map[string]any `json:"variables"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		vars = req.Variables
		io.WriteString(w, `{"data":{"Page":{"media":[{"id":1}]}}}`)
	})

	got, err := c.Season(context.Background(), "SUMMER", 2026, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d results", len(got))
	}
	if vars["season"] != "SUMMER" || vars["year"].(float64) != 2026 {
		t.Fatalf("vars = %v", vars)
	}
	if p := vars["perPage"].(float64); p > 50 {
		t.Fatalf("perPage = %v, exceeds the server-side cap", p)
	}
}
