package server

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"kuro/internal/config"
	"kuro/internal/store"
)

// AniList down, but kuro saved the show: the page opens from that, and says so.
func TestAnimePageFallsBackToSavedRecord(t *testing.T) {
	h := newHarness(t, config.Config{}, func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"errors":[{"message":"Internal Server Error","status":500}]}`)
	})
	english, status := "Frieren", "FINISHED"
	if _, err := h.store.ImportList(context.Background(), []store.Anime{{
		ID: 154587, Romaji: "Sousou no Frieren", English: &english, Status: &status,
	}}, nil, store.ImportMerge); err != nil {
		t.Fatal(err)
	}

	res, body := h.do(t, "GET", "/api/anime/154587")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %v", res.StatusCode, body)
	}
	if res.Header.Get(headerSaved) == "" {
		t.Error("a saved record was not flagged as saved")
	}
	anime, _ := body["anime"].(map[string]any)
	if title, _ := anime["title"].(map[string]any); title["english"] != "Frieren" {
		t.Errorf("anime = %v", anime)
	}

	// Nothing saved: still the error, not an empty page.
	if res, _ := h.do(t, "GET", "/api/anime/999"); res.StatusCode != http.StatusBadGateway {
		t.Errorf("unsaved show during outage: status %d", res.StatusCode)
	}
}

// Every show a Browse page returns is kept, and the page says it was live.
func TestBrowseKeepsWhatItShows(t *testing.T) {
	h := newHarness(t, config.Config{}, func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"data":{"Page":{"pageInfo":{"total":1,"hasNextPage":false},"media":[
			{"id":171018,"title":{"romaji":"Dandadan"},"status":"RELEASING","synonyms":[],"genres":["Action"],"studios":{"nodes":[{"id":6145,"name":"Science SARU"}]}}]}}}`)
	})

	res, body := h.do(t, "GET", "/api/browse?q=dandadan")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %v", res.StatusCode, body)
	}
	if res.Header.Get(headerLive) == "" || res.Header.Get(headerSaved) != "" {
		t.Errorf("live answer headers: %v", res.Header)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, _, ok, _ := h.store.AnimeRecord(context.Background(), 171018); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("browsed show never saved")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
