package engine

import (
	"net/http"
	"testing"

	"kuro/internal/torrent"
)

// Only kuro's own client, with the per-run secret, reaches the engine's API; a browser's request, which
// carries Origin or Sec-Fetch headers, is refused; a stream read by ffmpeg or mpv needs no secret.
func TestEngineAPIRefusesBrowsersAndStrangers(t *testing.T) {
	f := newFixture(t)
	base := "http://" + f.engine.Addr()
	ask := func(path string, headers map[string]string) int {
		req, _ := http.NewRequest(http.MethodGet, base+path, nil)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if code := ask("/torrents", nil); code != http.StatusForbidden {
		t.Errorf("no secret: %d, want 403", code)
	}
	if code := ask("/torrents", map[string]string{torrent.SecretHeader: "wrong"}); code != http.StatusForbidden {
		t.Errorf("wrong secret: %d, want 403", code)
	}
	if code := ask("/torrents", map[string]string{torrent.SecretHeader: f.engine.secret, "Origin": "http://evil"}); code != http.StatusForbidden {
		t.Errorf("a browser with the secret: %d, want 403", code)
	}
	if code := ask("/torrents", map[string]string{torrent.SecretHeader: f.engine.secret}); code != http.StatusOK {
		t.Errorf("kuro's own client: %d, want 200", code)
	}
	// A stream path with no secret gets past the guard; only the torrent is missing.
	if code := ask("/torrents/99/stream/0", nil); code != http.StatusNotFound {
		t.Errorf("a player's stream read: %d, want 404", code)
	}
	if code := ask("/torrents/99/stream/0", map[string]string{"Sec-Fetch-Mode": "no-cors"}); code != http.StatusForbidden {
		t.Errorf("a browser's stream read: %d, want 403", code)
	}
}
