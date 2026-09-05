package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"kuro/internal/config"
	"kuro/internal/store"
)

func (h *harness) postJSON(t *testing.T, target string, body any) (*http.Response, map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", target, strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "127.0.0.1:12345"
	req.Host = "127.0.0.1:4321"

	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)
	res := rec.Result()
	var out map[string]any
	if strings.HasPrefix(res.Header.Get("Content-Type"), "application/json") {
		json.NewDecoder(res.Body).Decode(&out)
	}
	return res, out
}

func seedShow(t *testing.T, h *harness, id, episodes int) {
	t.Helper()
	if _, err := h.store.ImportList(context.Background(),
		[]store.Anime{{ID: id, Romaji: "Show", Episodes: &episodes}}, nil, store.ImportMerge); err != nil {
		t.Fatal(err)
	}
}

func TestWatchedToggleByHand(t *testing.T) {
	h := newHarness(t, config.Config{}, nil)
	ctx := context.Background()
	seedShow(t, h, 1, 12)

	res, _ := h.postJSON(t, "/api/watched", map[string]any{"animeId": 1, "episode": 4})
	if res.StatusCode != 200 {
		t.Fatalf("mark: HTTP %d", res.StatusCode)
	}
	if e, _ := h.store.ListEntry(ctx, 1); e.Progress != 4 {
		t.Fatalf("progress = %d after marking 4", e.Progress)
	}

	res, body := h.postJSON(t, "/api/watched", map[string]any{"animeId": 1, "episode": 3, "watched": false})
	if res.StatusCode != 200 || body["watched"] != false {
		t.Fatalf("unmark: HTTP %d %v", res.StatusCode, body)
	}
	if e, _ := h.store.ListEntry(ctx, 1); e.Progress != 2 {
		t.Fatalf("progress = %d after unmarking 3, want 2", e.Progress)
	}

	res, _ = h.postJSON(t, "/api/watched", map[string]any{"animeId": 1})
	if res.StatusCode != 400 {
		t.Fatalf("missing episode: HTTP %d", res.StatusCode)
	}
}

func TestScoreEndpoint(t *testing.T) {
	h := newHarness(t, config.Config{}, nil)
	ctx := context.Background()
	seedShow(t, h, 1, 12)

	res, _ := h.postJSON(t, "/api/score", map[string]any{"animeId": 1, "score": 90})
	if res.StatusCode != 200 {
		t.Fatalf("HTTP %d", res.StatusCode)
	}
	if e, _ := h.store.ListEntry(ctx, 1); e.Score != 90 {
		t.Fatalf("score = %d", e.Score)
	}
	for _, bad := range []map[string]any{{"animeId": 1, "score": 101}, {"animeId": 1, "score": -1}, {"score": 50}} {
		if res, _ := h.postJSON(t, "/api/score", bad); res.StatusCode != 400 {
			t.Errorf("%v: HTTP %d, want 400", bad, res.StatusCode)
		}
	}
}

func TestBookmarkEndpoints(t *testing.T) {
	h := newHarness(t, config.Config{}, nil)
	seedShow(t, h, 1, 12)

	res, _ := h.postJSON(t, "/api/bookmarks", map[string]any{"animeId": 1, "favourite": true, "note": "gem"})
	if res.StatusCode != 200 {
		t.Fatalf("HTTP %d", res.StatusCode)
	}
	_, body := h.do(t, "GET", "/api/bookmarks")
	items, _ := body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("favourites = %v", body)
	}
	b, _ := h.store.Bookmark(context.Background(), 1)
	if !b.Favourite || b.Note != "gem" {
		t.Fatalf("bookmark = %+v", b)
	}
}

// Genres are AniList's, so a filtered draw is answered from there; a plain one
// stays local to the corpus.
func TestRandomWithGenresAsksAniList(t *testing.T) {
	var queries []string
	h := newHarness(t, config.Config{}, func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query string `json:"query"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		queries = append(queries, req.Query)
		switch {
		case strings.Contains(req.Query, "query Random"):
			io.WriteString(w, `{"data":{"Page":{"pageInfo":{"total":3},"media":[{"id":100,"title":{"romaji":"Pick"}}]}}}`)
		default:
			io.WriteString(w, `{"data":{"Page":{"media":[{"id":100,"title":{"romaji":"Pick"}}]}}}`)
		}
	})

	res, body := h.do(t, "GET", "/api/random?genres=Romance,Comedy&format=TV")
	if res.StatusCode != 200 || body["id"] != float64(100) {
		t.Fatalf("HTTP %d %v", res.StatusCode, body)
	}
	if len(queries) == 0 || !strings.Contains(queries[0], "query Random") {
		t.Fatalf("queries = %v", queries)
	}

	// Nothing in the corpus and no genres: one upstream draw, no genre filter.
	before := len(queries)
	res, body = h.do(t, "GET", "/api/random")
	if res.StatusCode != 200 || body["id"] != float64(100) || len(queries) == before {
		t.Fatalf("HTTP %d %v, %d new upstream calls", res.StatusCode, body, len(queries)-before)
	}
}

func TestAnimeDetailCarriesScoreAndBookmark(t *testing.T) {
	h := newHarness(t, config.Config{}, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"data":{"Page":{"media":[{"id":1,"title":{"romaji":"Show"}}]}}}`)
	})
	ctx := context.Background()
	seedShow(t, h, 1, 12)
	h.store.SetScore(ctx, 1, 70)
	h.store.SetBookmark(ctx, 1, store.Bookmark{Favourite: true, Note: "n"})

	_, body := h.do(t, "GET", "/api/anime/1")
	if body["score"] != float64(70) {
		t.Fatalf("score = %v", body["score"])
	}
	bm, _ := body["bookmark"].(map[string]any)
	if bm["favourite"] != true || bm["note"] != "n" {
		t.Fatalf("bookmark = %v", body["bookmark"])
	}
}
