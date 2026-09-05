package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"kuro/internal/config"
	"kuro/internal/indexer"
	"kuro/internal/library"
	"kuro/internal/store"
	"kuro/internal/torrent"
)

// Enough of rqbit for one play.
func fakeEngine(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	var ids []string

	mux.HandleFunc("GET /torrents", func(w http.ResponseWriter, r *http.Request) {
		held := []map[string]any{}
		for i, hash := range ids {
			held = append(held, map[string]any{"id": i + 1, "info_hash": hash, "name": hash})
		}
		json.NewEncoder(w).Encode(map[string]any{"torrents": held})
	})
	mux.HandleFunc("POST /torrents", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		magnet := string(body)
		hash := magnet[strings.Index(magnet, "btih:")+5:]
		if i := strings.IndexByte(hash, '&'); i >= 0 {
			hash = hash[:i]
		}
		id := 0
		if r.URL.Query().Get("list_only") != "true" {
			ids = append(ids, hash)
			id = len(ids)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"id": id,
			"details": map[string]any{
				"info_hash": hash, "name": "Slime S3 - 02.mkv",
				"files": []map[string]any{{"name": "Slime S3 - 02.mkv", "length": 1 << 30, "included": true}},
			},
		})
	})
	mux.HandleFunc("GET /torrents/{id}/stats/v1", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"state": "live", "total_bytes": 1 << 30})
	})
	mux.HandleFunc("GET /torrents/{id}/stream/{file}", func(w http.ResponseWriter, r *http.Request) {
		w.Write(make([]byte, 4<<20))
	})
	mux.HandleFunc("POST /torrents/{id}/{action}", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "{}")
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// The browser sends no season; the handler must not invent one.
func TestPlayWithoutASeasonPlaysTheTitlesSeason(t *testing.T) {
	h := newHarness(t, config.Config{}, nil)
	h.indexer.results = []indexer.Torrent{
		{Title: "[Trix] Tensei Shitara Slime Datta Ken S01+02+OADs+Tensura Nikki - AV1", InfoHash: strings.Repeat("a", 40), Seeders: 900, Size: 1 << 30, Category: "1_2"},
		{Title: "[Erai-raws] Tensei Shitara Slime Datta Ken 3rd Season - 02 [1080p][HEVC].mkv", InfoHash: strings.Repeat("c", 40), Seeders: 100, Size: 1 << 30, Category: "1_2"},
	}

	english, tv, episodes := "That Time I Got Reincarnated as a Slime Season 3", "TV", 24
	if _, err := h.store.ImportList(context.Background(), []store.Anime{{
		ID: 156822, Romaji: "Tensei shitara Slime Datta Ken 3rd Season", English: &english,
		Format: &tv, Episodes: &episodes, Synonyms: "[]", Genres: "[]",
	}}, nil, store.ImportMerge); err != nil {
		t.Fatal(err)
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	finder := library.NewFinder(h.store, h.indexer, log)
	h.server.playback = library.NewPlayback(h.store, finder, torrent.NewClient(fakeEngine(t).URL), nil, t.TempDir(), log)

	req := httptest.NewRequest("POST", "/api/play", strings.NewReader(`{"animeId":156822,"episode":2}`))
	req.RemoteAddr = "127.0.0.1:12345"
	req.Host = "127.0.0.1:4321"
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)

	var body map[string]any
	json.NewDecoder(rec.Body).Decode(&body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %v", rec.Code, body["error"])
	}
	if title, _ := body["title"].(string); !strings.Contains(title, "3rd Season") {
		t.Fatalf("played %q, want the season-three release", title)
	}
}
