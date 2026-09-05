package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"kuro/internal/config"
)

func spaFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":            {Data: []byte("<!doctype html><div id=root>")},
		"assets/app-abc123.js":  {Data: []byte("console.log(1)")},
		"assets/app-abc123.css": {Data: []byte("body{}")},
	}
}

func get(t *testing.T, h http.Handler, target string) *http.Response {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", target, nil)
	req.Host = "127.0.0.1:4321"
	h.ServeHTTP(rec, req)
	return rec.Result()
}

func TestSPAServesIndexForClientRoutes(t *testing.T) {
	h := spa(spaFS())

	for _, route := range []string{"/", "/watch/154587/10", "/settings", "/anime/16498"} {
		res := get(t, h, route)
		if res.StatusCode != http.StatusOK {
			t.Errorf("%s = %d, want the app shell", route, res.StatusCode)
		}
	}
}

// A missing asset used to answer with index.html. A module worker handed HTML
// fails to parse, which silently disabled subtitle rendering.
func TestSPAMissingAssetIsNotFound(t *testing.T) {
	h := spa(spaFS())

	for _, missing := range []string{
		"/assets/util.js",
		"/assets/renderers/2d-renderer.js",
		"/assets/gone-deadbeef.wasm",
	} {
		res := get(t, h, missing)
		if res.StatusCode != http.StatusNotFound {
			t.Errorf("%s = %d, want 404 rather than the app shell", missing, res.StatusCode)
		}
	}
}

func TestSPAServesRealAssets(t *testing.T) {
	h := spa(spaFS())

	res := get(t, h, "/assets/app-abc123.js")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	if cache := res.Header.Get("Cache-Control"); cache == "" {
		t.Error("hashed assets should be cacheable")
	}
}

// The app is served from a catch-all, so anything unrouted lands on index.html.
// These go through the real mux, where a swallowed route would show.
func TestAPIPathsNeverFallThroughToTheApp(t *testing.T) {
	h := newHarness(t, config.Config{}, nil)

	for _, path := range []string{
		"/api/health", "/api/settings", "/api/nonsense", "/api/",
	} {
		res, _ := h.do(t, "GET", path)
		if ct := res.Header.Get("Content-Type"); strings.HasPrefix(ct, "text/html") {
			t.Errorf("%s served the app (%s); an unknown API path must 404", path, ct)
		}
	}
}

// Both providers redirect into the app after signing in. A catch-all that
// swallowed either would break the connection silently.
func TestCallbackRoutesAreNotSwallowedByTheApp(t *testing.T) {
	h := newHarness(t, config.Config{}, nil)

	for _, path := range []string{"/callback?code=x", "/mal/callback?code=x"} {
		res, _ := h.do(t, "GET", path)
		if ct := res.Header.Get("Content-Type"); strings.HasPrefix(ct, "text/html") &&
			res.StatusCode == http.StatusOK {
			t.Errorf("%s was served the app instead of being handled", path)
		}
	}
}
