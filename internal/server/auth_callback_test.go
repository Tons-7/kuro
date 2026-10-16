package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"kuro/internal/config"
	"kuro/internal/library"
	"kuro/internal/store"
	"kuro/internal/torrent"
)

// The callback installs the new token only once the account is known and saved: a failure halfway
// leaves the client as it was, not carrying a token Settings says is not connected.
func TestAniListCallbackInstallsTheTokenOnlyOnceSaved(t *testing.T) {
	viewerFails := true
	h := newHarness(t, config.Config{Addr: "127.0.0.1:4321"}, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			io.WriteString(w, `{"access_token":"fresh","expires_in":3600}`)
			return
		}
		if viewerFails {
			w.WriteHeader(http.StatusForbidden)
			io.WriteString(w, `{"errors":[{"message":"temporarily disabled","status":403}]}`)
			return
		}
		if r.Header.Get("Authorization") != "Bearer fresh" {
			t.Errorf("viewer asked with %q, want the new token", r.Header.Get("Authorization"))
		}
		io.WriteString(w, `{"data":{"Viewer":{"id":7,"name":"tony","mediaListOptions":{"scoreFormat":"POINT_100"}}}}`)
	})
	ctx := t.Context()
	h.store.SetSetting(ctx, "anilist.client_id", "1")

	callback := func() int {
		h.store.SetSetting(ctx, "anilist.state", "s")
		res, _ := h.do(t, "GET", "/callback?state=s&code=c")
		return res.StatusCode
	}

	if code := callback(); code != http.StatusBadGateway {
		t.Fatalf("status = %d, want the viewer failure", code)
	}
	if h.server.anilist.Authenticated() {
		t.Fatal("a half-finished login left its token on the live client")
	}
	if tok, _ := h.store.Setting(ctx, "anilist.token"); tok != "" {
		t.Fatalf("stored token = %q", tok)
	}

	viewerFails = false
	if code := callback(); code != http.StatusFound {
		t.Fatalf("status = %d", code)
	}
	if !h.server.anilist.Authenticated() {
		t.Fatal("a finished login should install the token")
	}
	if name, _ := h.store.Setting(ctx, "anilist.user_name"); name != "tony" {
		t.Fatalf("user = %q", name)
	}
}

// The reaper taking a session nobody closed releases the episode the way a close does: unpinned, so the
// cache may take it, and the queue's hold is given back.
func TestIdleSessionReleasesTheEpisode(t *testing.T) {
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"torrents":[]}`)
	}))
	t.Cleanup(engine.Close)
	h := newHarness(t, config.Config{Addr: "127.0.0.1:4321"}, nil, func(d *Deps) {
		log := slog.New(slog.NewTextHandler(io.Discard, nil))
		d.Playback = library.NewPlayback(d.Store, nil, torrent.NewClient(engine.URL), nil, t.TempDir(), log)
	})
	ctx := t.Context()
	if err := h.store.RecordTorrent(ctx, store.TorrentRecord{
		InfoHash: "abc", EngineID: 1, Name: "x", AnimeID: 1, EpKey: "5", FilePath: "x.mkv", TotalSize: 100,
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.store.PinOnly(ctx, "abc", 0); err != nil {
		t.Fatal(err)
	}

	h.server.SessionIdle("1-5")
	entries, _ := h.store.CacheEntries(ctx)
	if len(entries) != 1 || entries[0].Pinned {
		t.Fatalf("entries = %+v, want the episode unpinned", entries)
	}
}
