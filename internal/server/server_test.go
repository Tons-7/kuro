package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"kuro/internal/anilist"
	"kuro/internal/config"
	"kuro/internal/corpus"
	"kuro/internal/db"
	"kuro/internal/indexer"
	"kuro/internal/library"
	"kuro/internal/store"
)

type harness struct {
	handler http.Handler
	server  *Server
	store   *store.Store
	indexer *stubIndexer
	calls   int
}

func newHarness(t *testing.T, cfg config.Config, upstream http.HandlerFunc, extra ...func(*Deps)) *harness {
	t.Helper()

	conn, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := conn.Migrate(); err != nil {
		t.Fatal(err)
	}

	h := &harness{store: store.New(conn), indexer: &stubIndexer{}}
	if upstream == nil {
		upstream = func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"data":{}}`) }
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.calls++
		upstream(w, r)
	}))
	t.Cleanup(srv.Close)

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	al := anilist.New(log,
		anilist.WithEndpoint(srv.URL),
		anilist.WithOAuthBase(srv.URL),
		anilist.WithoutRateLimit())

	h.indexer = &stubIndexer{}
	deps := Deps{
		Config:   cfg,
		Store:    h.store,
		AniList:  al,
		Importer: library.NewImporter(h.store, al, log),
		Ingester: library.NewIngester(h.store, corpus.NewFetcher(), al, log),
		Indexer:  h.indexer,
		Log:      log,
	}
	for _, f := range extra {
		f(&deps)
	}
	h.server = New(deps)
	h.handler = h.server.Handler()
	return h
}

type stubIndexer struct {
	results []indexer.Torrent
	err     error
}

func (s *stubIndexer) Name() string { return "stub" }
func (s *stubIndexer) Search(context.Context, indexer.Query) ([]indexer.Torrent, error) {
	return s.results, s.err
}

func (h *harness) do(t *testing.T, method, target string) (*http.Response, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	// httptest defaults to a public address, which the access guard rejects.
	req.RemoteAddr = "127.0.0.1:12345"
	req.Host = "127.0.0.1:4321"

	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)

	res := rec.Result()
	var body map[string]any
	if strings.HasPrefix(res.Header.Get("Content-Type"), "application/json") {
		if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
			t.Fatalf("%s %s: decode: %v", method, target, err)
		}
	}
	return res, body
}

func TestHealth(t *testing.T) {
	h := newHarness(t, config.Config{}, nil)
	res, body := h.do(t, "GET", "/api/health")

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	if body["ok"] != true {
		t.Fatalf("ok = %v", body["ok"])
	}
	if body["anilist"] != false {
		t.Fatalf("anilist should report disconnected, got %v", body["anilist"])
	}
	if body["settings"].(float64) < 10 {
		t.Fatalf("settings = %v, want the seeded defaults", body["settings"])
	}
}

// Short queries must not reach the network: AniList allows 30 requests a
// minute and fuzzy matching returns nothing useful below three characters.
func TestSearchSkipsUpstreamForShortQueries(t *testing.T) {
	h := newHarness(t, config.Config{}, nil)

	for _, q := range []string{"", "f", "fr"} {
		res, body := h.do(t, "GET", "/api/search?q="+q)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("%q: status %d", q, res.StatusCode)
		}
		if got := body["results"].([]any); len(got) != 0 {
			t.Fatalf("%q returned %d results", q, len(got))
		}
	}
	if h.calls != 0 {
		t.Fatalf("%d upstream calls made for short queries", h.calls)
	}
}

func TestSearchReturnsResults(t *testing.T) {
	h := newHarness(t, config.Config{}, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"data":{"Page":{"media":[
		  {"id":154587,"title":{"romaji":"Sousou no Frieren"},"episodes":28}]}}}`)
	})

	res, body := h.do(t, "GET", "/api/search?q=frieren")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	results := body["results"].([]any)
	if len(results) != 1 {
		t.Fatalf("got %d results", len(results))
	}
	if got := results[0].(map[string]any)["id"].(float64); got != 154587 {
		t.Fatalf("id = %v", got)
	}
}

func TestSearchSurfacesUpstreamFailure(t *testing.T) {
	h := newHarness(t, config.Config{}, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"errors":[{"message":"down","status":503}]}`)
	})

	res, body := h.do(t, "GET", "/api/search?q=frieren")
	if res.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", res.StatusCode)
	}
	if body["error"] == nil {
		t.Fatal("no error message returned")
	}
}

func TestLibraryEmptyReturnsArrayNotNull(t *testing.T) {
	h := newHarness(t, config.Config{}, nil)
	_, body := h.do(t, "GET", "/api/library")

	if body["total"].(float64) != 0 {
		t.Fatalf("total = %v", body["total"])
	}
	// A JSON null here would break the frontend's .map().
	if _, ok := body["items"].([]any); !ok {
		t.Fatalf("items = %#v, want an array", body["items"])
	}
	if body["hasMore"] != false {
		t.Errorf("hasMore = %v on an empty library", body["hasMore"])
	}
}

// The frontend needs page size and total to render controls at all.
func TestLibraryReturnsPagingEnvelope(t *testing.T) {
	h := newHarness(t, config.Config{}, nil)
	_, body := h.do(t, "GET", "/api/library?page=2&perPage=25")

	if body["page"].(float64) != 2 {
		t.Errorf("page = %v", body["page"])
	}
	if body["perPage"].(float64) != 25 {
		t.Errorf("perPage = %v", body["perPage"])
	}
}

// An unbounded perPage would let one request pull the entire corpus.
func TestPerPageIsClamped(t *testing.T) {
	h := newHarness(t, config.Config{}, nil)
	_, body := h.do(t, "GET", "/api/library?perPage=100000")

	if got := body["perPage"].(float64); got > 200 {
		t.Fatalf("perPage = %v, want it clamped", got)
	}
}

func TestTorrentsRequiresQuery(t *testing.T) {
	h := newHarness(t, config.Config{}, nil)
	res, body := h.do(t, "GET", "/api/torrents")

	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", res.StatusCode)
	}
	if body["error"] == nil {
		t.Fatal("no error message returned")
	}
}

// Each result carries its rebuilt magnet and the parsed release fields, so the
// UI can filter by group and quality without parsing titles itself.
func TestTorrentsEnrichesResults(t *testing.T) {
	h := newHarness(t, config.Config{}, nil)
	h.indexer.results = []indexer.Torrent{{
		Title:    "[SubsPlease] Sousou no Frieren - 10 (1080p) [7D35515E].mkv",
		InfoHash: "abc123",
		Seeders:  189,
		Size:     1503238553,
	}}

	res, body := h.do(t, "GET", "/api/torrents?q=frieren")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}

	results := body["results"].([]any)
	if len(results) != 1 {
		t.Fatalf("got %d results", len(results))
	}
	first := results[0].(map[string]any)

	magnet, _ := first["magnet"].(string)
	if !strings.HasPrefix(magnet, "magnet:?xt=urn:btih:abc123") {
		t.Errorf("magnet = %q", magnet)
	}

	parsed, ok := first["parsed"].(map[string]any)
	if !ok {
		t.Fatal("parsed fields missing")
	}
	if parsed["Group"] != "SubsPlease" {
		t.Errorf("group = %v", parsed["Group"])
	}
	if parsed["Episode"].(float64) != 10 {
		t.Errorf("episode = %v", parsed["Episode"])
	}
	if parsed["Resolution"] != "1080p" {
		t.Errorf("resolution = %v", parsed["Resolution"])
	}
}

func TestTorrentsSurfacesIndexerFailure(t *testing.T) {
	h := newHarness(t, config.Config{}, nil)
	h.indexer.err = io.ErrUnexpectedEOF

	res, _ := h.do(t, "GET", "/api/torrents?q=frieren")
	if res.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", res.StatusCode)
	}
}

func TestSettingsExposesSeededDefaults(t *testing.T) {
	h := newHarness(t, config.Config{}, nil)
	_, body := h.do(t, "GET", "/api/settings")

	if got := body["cache.budget_bytes"]; got != store.Defaults["cache.budget_bytes"] {
		t.Fatalf("cache budget = %v, want %q", got, store.Defaults["cache.budget_bytes"])
	}
}

func TestAuthLoginRequiresCredentials(t *testing.T) {
	h := newHarness(t, config.Config{}, nil)
	res, body := h.do(t, "GET", "/api/auth/login")

	if res.StatusCode != http.StatusPreconditionFailed {
		t.Fatalf("status = %d, want 412", res.StatusCode)
	}
	if body["redirect"] == nil {
		t.Fatal("response should tell the user which redirect URL to register")
	}
}

func TestAuthLoginRedirectsWithState(t *testing.T) {
	cfg := config.Config{Addr: "127.0.0.1:4321"}
	cfg.AniList.ClientID = "1234"
	h := newHarness(t, cfg, nil)

	res, _ := h.do(t, "GET", "/api/auth/login")
	if res.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302", res.StatusCode)
	}

	location := res.Header.Get("Location")
	for _, want := range []string{"client_id=1234", "response_type=code", "state=", "localhost%3A4321%2Fcallback"} {
		if !strings.Contains(location, want) {
			t.Errorf("redirect missing %q: %s", want, location)
		}
	}

	saved, _ := h.store.Setting(context.Background(), "anilist.state")
	if saved == "" {
		t.Fatal("state was not persisted for later verification")
	}
	if !strings.Contains(location, "state="+saved) {
		t.Fatal("redirect state does not match the stored value")
	}
}

func TestCallbackRejectsBadState(t *testing.T) {
	h := newHarness(t, config.Config{}, nil)
	h.store.SetSetting(context.Background(), "anilist.state", "expected")

	res, _ := h.do(t, "GET", "/callback?code=abc&state=forged")
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a mismatched state", res.StatusCode)
	}
}

// An empty stored state must not authorise a callback that also omits it.
func TestCallbackRejectsMissingState(t *testing.T) {
	h := newHarness(t, config.Config{}, nil)

	res, _ := h.do(t, "GET", "/callback?code=abc")
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", res.StatusCode)
	}
}

func TestCallbackStoresTokenAndViewer(t *testing.T) {
	h := newHarness(t, config.Config{}, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			io.WriteString(w, `{"token_type":"Bearer","expires_in":31536000,"access_token":"jwt-here"}`)
			return
		}
		io.WriteString(w, `{"data":{"Viewer":{"id":42,"name":"tony","mediaListOptions":{"scoreFormat":"POINT_100"}}}}`)
	})

	ctx := context.Background()
	h.store.SetSetting(ctx, "anilist.state", "expected")

	// The callback redirects back into the app rather than leaving the user on
	// a page telling them to close the tab.
	res, _ := h.do(t, "GET", "/callback?code=abc&state=expected")
	if res.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want a redirect", res.StatusCode)
	}
	if got := res.Header.Get("Location"); got != "/settings?connected=anilist" {
		t.Fatalf("redirected to %q", got)
	}

	want := map[string]string{
		"anilist.token":     "jwt-here",
		"anilist.user_id":   "42",
		"anilist.user_name": "tony",
		"anilist.score_fmt": "POINT_100",
	}
	for k, v := range want {
		if got, _ := h.store.Setting(ctx, k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}

	// Reusing a consumed state must not authorise a second callback.
	if got, _ := h.store.Setting(ctx, "anilist.state"); got != "" {
		t.Errorf("state was not cleared, got %q", got)
	}
	if expiry, _ := h.store.SettingInt(ctx, "anilist.expires_at"); expiry == 0 {
		t.Error("expiry not recorded; the yearly re-auth warning depends on it")
	}

	// The token must be live for subsequent requests without a restart.
	_, health := h.do(t, "GET", "/api/health")
	if health["anilist"] != true {
		t.Error("client did not pick up the new token")
	}
}

func TestCallbackRejectsMissingCode(t *testing.T) {
	h := newHarness(t, config.Config{}, nil)
	h.store.SetSetting(context.Background(), "anilist.state", "expected")

	res, _ := h.do(t, "GET", "/callback?state=expected")
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", res.StatusCode)
	}
}

func TestCallbackSurfacesExchangeFailure(t *testing.T) {
	h := newHarness(t, config.Config{}, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":"invalid_client","message":"Client authentication failed"}`)
	})
	h.store.SetSetting(context.Background(), "anilist.state", "expected")

	res, _ := h.do(t, "GET", "/callback?code=abc&state=expected")
	if res.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", res.StatusCode)
	}
	if token, _ := h.store.Setting(context.Background(), "anilist.token"); token != "" {
		t.Fatal("a token was stored despite the exchange failing")
	}
}

func TestSyncRequiresConnection(t *testing.T) {
	h := newHarness(t, config.Config{}, nil)
	res, body := h.do(t, "POST", "/api/sync")

	if res.StatusCode != http.StatusPreconditionFailed {
		t.Fatalf("status = %d, want 412", res.StatusCode)
	}
	if body["error"] == nil {
		t.Fatal("no error message returned")
	}
}

func TestSyncImportsList(t *testing.T) {
	h := newHarness(t, config.Config{}, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"data":{"MediaListCollection":{"lists":[{"entries":[
		  {"id":1,"mediaId":100,"progress":7,"status":"CURRENT",
		   "media":{"id":100,"title":{"romaji":"Frieren"}}}]}]}}}`)
	})
	h.store.SetSetting(context.Background(), "anilist.user_id", "42")

	res, body := h.do(t, "POST", "/api/sync")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %v", res.StatusCode, body)
	}
	if body["entries"].(float64) != 1 {
		t.Fatalf("entries = %v", body["entries"])
	}

	_, lib := h.do(t, "GET", "/api/library")
	if lib["total"].(float64) != 1 {
		t.Fatalf("library total = %v", lib["total"])
	}
}

func TestMethodAndRouteMismatches(t *testing.T) {
	h := newHarness(t, config.Config{}, nil)

	tests := []struct {
		method, target string
		want           int
	}{
		{"GET", "/api/sync", http.StatusMethodNotAllowed},
		{"POST", "/api/health", http.StatusMethodNotAllowed},
		{"GET", "/api/nope", http.StatusNotFound},
	}

	for _, tt := range tests {
		req := httptest.NewRequest(tt.method, tt.target, nil)
		req.RemoteAddr = "127.0.0.1:12345"
		req.Host = "127.0.0.1:4321"

		rec := httptest.NewRecorder()
		h.handler.ServeHTTP(rec, req)
		if rec.Code != tt.want {
			t.Errorf("%s %s = %d, want %d", tt.method, tt.target, rec.Code, tt.want)
		}
	}
}
