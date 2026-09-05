package server

import (
	"context"
	"testing"

	"kuro/internal/config"
	"kuro/internal/metadata"
	"kuro/internal/store"
)

func TestLocalSearchAnswersFromTheLibrary(t *testing.T) {
	h := newHarness(t, config.Config{}, nil)
	ctx := context.Background()
	en := "Frieren: Beyond Journey's End"
	h.store.ImportList(ctx, []store.Anime{
		{ID: 1, Romaji: "Sousou no Frieren", English: &en},
		{ID: 2, Romaji: "Chainsaw Man"},
	}, []store.Entry{{ID: 9, AnimeID: 1, Status: new(string("CURRENT")), Progress: 4}}, store.ImportMerge)

	_, body := h.do(t, "GET", "/api/search/local?q=frie")
	results, _ := body["results"].([]any)
	if len(results) != 1 {
		t.Fatalf("results = %v", body)
	}
	hit := results[0].(map[string]any)
	if hit["id"] != float64(1) || hit["title"] != en || hit["progress"] != float64(4) {
		t.Fatalf("hit = %v", hit)
	}
	if h.calls != 0 {
		t.Fatal("local search must not touch AniList")
	}

	_, body = h.do(t, "GET", "/api/search/local?q=")
	if results, _ := body["results"].([]any); len(results) != 0 {
		t.Fatalf("empty query = %v", body)
	}
}

func TestNextEpisodeEndpoint(t *testing.T) {
	h := newHarness(t, config.Config{}, nil)
	ctx := context.Background()
	mal, eps := 900, 3
	h.store.ImportList(ctx, []store.Anime{{ID: 1, Romaji: "Show", MalID: &mal, Episodes: &eps}}, nil, store.ImportMerge)
	h.store.SaveEpisodes(ctx, 1, []metadata.Episode{{Number: 1}, {Number: 2}, {Number: 3}})
	h.store.SaveFillers(ctx, []metadata.Filler{{MalID: 900, Episode: 2, Kind: "filler"}})

	_, body := h.do(t, "GET", "/api/episodes/next?id=1&after=1")
	if body["next"] != float64(2) {
		t.Fatalf("next = %v", body["next"])
	}
	h.postJSON(t, "/api/prefs", map[string]any{"key": "playback.skip_filler", "value": "true", "animeId": 1})
	_, body = h.do(t, "GET", "/api/episodes/next?id=1&after=1")
	if body["next"] != float64(3) {
		t.Fatalf("with skip: next = %v", body["next"])
	}
	_, body = h.do(t, "GET", "/api/episodes/next?id=1&after=3")
	if body["next"] != float64(0) {
		t.Fatalf("past the end: next = %v", body["next"])
	}
	if res, _ := h.do(t, "GET", "/api/episodes/next?after=1"); res.StatusCode != 400 {
		t.Fatalf("missing id: HTTP %d", res.StatusCode)
	}
}

func TestWatchStatsEndpoint(t *testing.T) {
	h := newHarness(t, config.Config{}, nil)
	seedShow(t, h, 1, 12)
	for range 3 {
		h.store.SavePlayback(context.Background(), store.PlaybackState{AnimeID: 1, EpKey: "1", Position: 100, Duration: 1440, Played: 60})
	}
	_, body := h.do(t, "GET", "/api/history/stats")
	if body["totalSeconds"] != float64(180) || body["weekSeconds"] != float64(180) {
		t.Fatalf("stats = %v", body)
	}
	if days, _ := body["days"].([]any); len(days) != 30 {
		t.Fatalf("days = %d", len(days))
	}
}

func TestLibraryAcceptsSortAndQuery(t *testing.T) {
	h := newHarness(t, config.Config{}, nil)
	ctx := context.Background()
	h.store.ImportList(ctx, []store.Anime{{ID: 1, Romaji: "Beta"}, {ID: 2, Romaji: "Alpha"}}, []store.Entry{
		{ID: 1, AnimeID: 1, Status: new(string("CURRENT")), Score: 30},
		{ID: 2, AnimeID: 2, Status: new(string("CURRENT")), Score: 80},
	}, store.ImportMerge)

	_, body := h.do(t, "GET", "/api/library?sort=title")
	items, _ := body["items"].([]any)
	if len(items) != 2 || items[0].(map[string]any)["id"] != float64(2) {
		t.Fatalf("title sort = %v", body)
	}
	_, body = h.do(t, "GET", "/api/library?sort=score")
	items, _ = body["items"].([]any)
	if items[0].(map[string]any)["id"] != float64(2) || items[0].(map[string]any)["score"] != float64(80) {
		t.Fatalf("score sort = %v", body)
	}
	_, body = h.do(t, "GET", "/api/library?q=bet")
	items, _ = body["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != float64(1) || body["total"] != float64(1) {
		t.Fatalf("query = %v", body)
	}
}
