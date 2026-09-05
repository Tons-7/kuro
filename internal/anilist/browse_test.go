package anilist

import (
	"context"
	"io"
	"net/http"
	"testing"
)

func browseVars(t *testing.T, b Browse) map[string]any {
	t.Helper()
	var vars map[string]any
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Variables map[string]any `json:"variables"`
		}
		decode(t, r, &req)
		vars = req.Variables
		io.WriteString(w, `{"data":{"Page":{"pageInfo":{},"media":[]}}}`)
	})
	if _, err := c.BrowseMedia(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	return vars
}

// Every hentai title is adult, so the old isAdult:false alongside the genre
// returned an empty page for the one filter that asks for it.
func TestBrowseHentaiGenreIncludesAdultTitles(t *testing.T) {
	if got := browseVars(t, Browse{Genres: []string{"Action"}})["isAdult"]; got != false {
		t.Fatalf("isAdult = %v for a normal genre, want false", got)
	}
	if got := browseVars(t, Browse{Genres: []string{"Romance", "hentai"}})["isAdult"]; got != true {
		t.Fatalf("isAdult = %v with Hentai chosen, want true", got)
	}
}

func TestRandomSendsFiltersAsVariables(t *testing.T) {
	var vars map[string]any
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Variables map[string]any `json:"variables"`
		}
		decode(t, r, &req)
		vars = req.Variables
		io.WriteString(w, `{"data":{"Page":{"pageInfo":{"total":40},"media":[{"id":5}]}}}`)
	})

	m, err := c.Random(context.Background(), "tv", []string{"Romance", "Comedy"}, 2019)
	if err != nil || m.ID != 5 {
		t.Fatalf("media=%+v err=%v", m, err)
	}
	if vars["format"] != "TV" || vars["year"] != float64(2019) || vars["isAdult"] != false {
		t.Fatalf("vars = %v", vars)
	}
	if g, _ := vars["genres"].([]any); len(g) != 2 {
		t.Fatalf("genres = %v", vars["genres"])
	}
	if page := vars["page"].(float64); page < 1 || page > 500 {
		t.Fatalf("page = %v", page)
	}

	// The total is remembered, so the next draw lands inside it.
	c.Random(context.Background(), "tv", []string{"Romance", "Comedy"}, 2019)
	if page := vars["page"].(float64); page < 1 || page > 40 {
		t.Fatalf("second page = %v, want within the known total of 40", page)
	}
}

func TestRandomHentaiDropsThePopularityFloor(t *testing.T) {
	var vars map[string]any
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Variables map[string]any `json:"variables"`
		}
		decode(t, r, &req)
		vars = req.Variables
		io.WriteString(w, `{"data":{"Page":{"pageInfo":{"total":1},"media":[{"id":9}]}}}`)
	})
	if _, err := c.Random(context.Background(), "", []string{"Hentai"}, 0); err != nil {
		t.Fatal(err)
	}
	if vars["isAdult"] != true || vars["floor"] != float64(0) {
		t.Fatalf("vars = %v", vars)
	}
	if _, present := vars["format"]; present {
		t.Fatal("empty format was sent")
	}
}

func TestRandomReportsNoMatch(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"data":{"Page":{"pageInfo":{"total":0},"media":[]}}}`)
	})
	if _, err := c.Random(context.Background(), "", []string{"Nothing"}, 0); err == nil {
		t.Fatal("expected an error when nothing matches")
	}
}
