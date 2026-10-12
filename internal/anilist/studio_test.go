package anilist

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// Every page is fetched in order at the largest size, anime only: the studio query has no filter to narrow by.
func TestStudioWorksFetchesEveryPage(t *testing.T) {
	var mu sync.Mutex
	var pages []int
	var perPage, sort any
	var query string
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		decode(t, r, &req)
		page := int(req.Variables["page"].(float64))
		mu.Lock()
		pages = append(pages, page)
		perPage, sort, query = req.Variables["perPage"], req.Variables["sort"], req.Query
		mu.Unlock()
		fmt.Fprintf(w, `{"data":{"Studio":{"name":"MAPPA","media":{"pageInfo":{"total":5,"hasNextPage":%v},"nodes":[
			{"id":%d,"title":{"romaji":"Show %d"},"type":"ANIME"},
			{"id":%d,"title":{"romaji":"Manga %d"},"type":"MANGA"}]}}}}`, page < 3, page*10, page, page*10+1, page)
	})

	name, works, err := c.StudioWorks(context.Background(), 569)
	if err != nil {
		t.Fatal(err)
	}
	if name != "MAPPA" || len(works) != 3 || works[0].ID != 10 || works[2].ID != 30 {
		t.Fatalf("name %q, works %v; want the three anime in page order", name, works)
	}
	if fmt.Sprint(pages) != "[1 2 3]" || perPage != 50.0 || fmt.Sprint(sort) != "[POPULARITY_DESC]" {
		t.Errorf("asked pages %v at %v sorted %v", pages, perPage, sort)
	}
	if !strings.Contains(query, "isMain: true") {
		t.Error("the query does not restrict the studio to its main works")
	}
}

// The largest studios run to hundreds of works; the fetch stops at the cap rather than paging forever.
func TestStudioWorksStopsAtTheCap(t *testing.T) {
	old := studioPagesCap
	studioPagesCap = 2
	t.Cleanup(func() { studioPagesCap = old })
	var mu sync.Mutex
	calls := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		io.Copy(io.Discard, r.Body)
		io.WriteString(w, `{"data":{"Studio":{"name":"Toei","media":{"pageInfo":{"total":900,"hasNextPage":true},"nodes":[{"id":1,"type":"ANIME"}]}}}}`)
	})
	if _, works, err := c.StudioWorks(context.Background(), 18); err != nil || len(works) != 2 || calls != 2 {
		t.Fatalf("works %d, calls %d, err %v; want the two pages the cap allows", len(works), calls, err)
	}
}
