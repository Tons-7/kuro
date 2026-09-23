package server

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"kuro/internal/config"
)

// A studio's works come from the studio; the other filters narrow the page.
func TestBrowseByStudio(t *testing.T) {
	h := newHarness(t, config.Config{}, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "StudioMedia") {
			io.WriteString(w, `{"data":{}}`)
			return
		}
		io.WriteString(w, `{"data":{"Studio":{"name":"MAPPA","media":{
		  "pageInfo":{"total":3,"hasNextPage":false},
		  "nodes":[
		    {"id":1,"title":{"romaji":"Show A"},"format":"TV","genres":["Action"],"type":"ANIME",
		     "studios":{"nodes":[{"id":569,"name":"MAPPA"}]},"startDate":{"year":2022,"month":10,"day":12}},
		    {"id":2,"title":{"romaji":"Film B"},"format":"MOVIE","genres":["Action"],"type":"ANIME"},
		    {"id":3,"title":{"romaji":"Manga C"},"format":"MANGA","genres":["Action"],"type":"MANGA"}]}}}}`)
	})

	res, body := h.do(t, "GET", "/api/browse?studio=569&formats=TV")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %v", res.StatusCode, body)
	}
	items, _ := body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items = %v, want only the TV series", items)
	}
	card := items[0].(map[string]any)
	if card["startDate"] != "2022-10-12" {
		t.Errorf("startDate = %v", card["startDate"])
	}
	if studios, _ := card["studios"].([]any); len(studios) != 1 {
		t.Errorf("studios = %v", card["studios"])
	}
	if s, _ := body["studio"].(map[string]any); s["name"] != "MAPPA" {
		t.Errorf("studio = %v", body["studio"])
	}
}
