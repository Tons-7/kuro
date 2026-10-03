package anilist

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func sentVars(t *testing.T, b Browse) map[string]any {
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
	if got := sentVars(t, Browse{Genres: []string{"Action"}})["isAdult"]; got != false {
		t.Fatalf("isAdult = %v for a normal genre, want false", got)
	}
	if got := sentVars(t, Browse{Genres: []string{"Romance", "hentai"}})["isAdult"]; got != true {
		t.Fatalf("isAdult = %v with Hentai chosen, want true", got)
	}
}

func sortVar(t *testing.T, vars map[string]any) string {
	t.Helper()
	list, ok := vars["sort"].([]any)
	if !ok || len(list) != 1 {
		t.Fatalf("sort = %#v", vars["sort"])
	}
	return list[0].(string)
}

// Choosing a sort used to be dropped whenever a search term was present, so
// the Browse page ignored every sort once something was typed.
func TestBrowseKeepsTheSortTheUserChose(t *testing.T) {
	for _, tc := range []struct {
		name string
		b    Browse
		want string
	}{
		{"nothing chosen", Browse{}, "POPULARITY_DESC"},
		{"a sort alone", Browse{Sort: "score"}, "SCORE_DESC"},
		{"a search alone falls back to relevance", Browse{Search: "conan"}, "SEARCH_MATCH"},
		{"searching and sorting", Browse{Search: "conan", Sort: "score"}, "SCORE_DESC"},
		{"with the other filters", Browse{Search: "conan", Formats: []string{"MOVIE"}, Sort: "newest"}, "START_DATE_DESC"},
		{"an unknown sort is not honoured", Browse{Search: "conan", Sort: "nonsense"}, "SEARCH_MATCH"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := sortVar(t, sentVars(t, tc.b)); got != tc.want {
				t.Errorf("sort = %s, want %s", got, tc.want)
			}
		})
	}
}

// AniList reports a fixed total of 5000 for any paged answer; the count comes
// from one-result probes, found exactly in a couple of requests.
func TestBrowseCountFindsTheRealTotal(t *testing.T) {
	alias := regexp.MustCompile(`p(\d+): Page\(page: (\d+), perPage: 1\)`)
	for _, real := range []int{1, 43, 84, 85, 300, 4999, 5000, 7000} {
		requests := 0
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			requests++
			var req struct {
				Query     string         `json:"query"`
				Variables map[string]any `json:"variables"`
			}
			decode(t, r, &req)
			if _, ok := req.Variables["page"]; ok {
				t.Error("probe sent a page variable its query does not declare")
			}
			parts := []string{}
			for _, m := range alias.FindAllStringSubmatch(req.Query, -1) {
				k, _ := strconv.Atoi(m[2])
				exists, more := k <= min(real, BrowseCap), k < min(real, BrowseCap)
				media := "[]"
				if exists {
					media = `[{"id":1}]`
				}
				parts = append(parts, fmt.Sprintf(`"p%s":{"pageInfo":{"hasNextPage":%t},"media":%s}`, m[1], more, media))
			}
			io.WriteString(w, `{"data":{`+strings.Join(parts, ",")+`}}`)
		})
		got, err := c.BrowseCount(context.Background(), Browse{Genres: []string{"Romance"}}, 1)
		if err != nil {
			t.Fatal(err)
		}
		if want := min(real, BrowseCap); got != want {
			t.Errorf("%d results: counted %d", real, got)
		}
		if requests > 4 {
			t.Errorf("%d results took %d requests", real, requests)
		}
	}
}

func TestBrowseCountWaitsWhileTheBudgetIsBusy(t *testing.T) {
	c := testClient(t, func(http.ResponseWriter, *http.Request) { t.Error("counted with no budget to spare") })
	c.limiter = rate.NewLimiter(rate.Every(2*time.Second), 5)
	c.limiter.AllowN(time.Now(), 3)
	if _, err := c.BrowseCount(context.Background(), Browse{}, 1); !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
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
