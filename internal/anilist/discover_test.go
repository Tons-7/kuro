package anilist

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestSeasonAndTheOneAfterIt(t *testing.T) {
	for _, tc := range []struct {
		month          time.Month
		season, next   string
		nextInNextYear bool
	}{
		{time.January, "WINTER", "SPRING", false},
		{time.March, "WINTER", "SPRING", false},
		{time.April, "SPRING", "SUMMER", false},
		{time.June, "SPRING", "SUMMER", false},
		{time.July, "SUMMER", "FALL", false},
		{time.September, "SUMMER", "FALL", false},
		{time.October, "FALL", "WINTER", true},
		// The last day of the year: next season is next year's winter.
		{time.December, "FALL", "WINTER", true},
	} {
		at := time.Date(2026, tc.month, 28, 12, 0, 0, 0, time.UTC)
		if season, year := Season(at); season != tc.season || year != 2026 {
			t.Errorf("%s is %s %d, want %s 2026", tc.month, season, year, tc.season)
		}
		wantYear := 2026
		if tc.nextInNextYear {
			wantYear = 2027
		}
		if season, year := NextSeason(at); season != tc.next || year != wantYear {
			t.Errorf("after %s comes %s %d, want %s %d", tc.month, season, year, tc.next, wantYear)
		}
	}
}

func TestRisingWindowsAreTheComputedSortsOnly(t *testing.T) {
	if days, ok := RisingWindow("WEEK"); !ok || days != 7 {
		t.Errorf("week = %d, %v", days, ok)
	}
	if days, ok := RisingWindow(SortMonth); !ok || days != 30 {
		t.Errorf("month = %d, %v", days, ok)
	}
	if _, ok := RisingWindow(SortTrending); ok {
		t.Error("trending is AniList's own ranking, not a computed window")
	}
	// Every sort the app offers is either computed here or one AniList can order by.
	for _, s := range Sorts() {
		_, computed := RisingWindow(s)
		if _, upstream := sorts[s]; !computed && !upstream {
			t.Errorf("sort %q is offered but nothing can answer it", s)
		}
	}
}

// Each ranking is the same query with its own order and filter.
func TestDiscoverAsksForTheRankingItWasGiven(t *testing.T) {
	var query string
	var vars map[string]any
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		decode(t, r, &req)
		query, vars = req.Query, req.Variables
		io.WriteString(w, `{"data":{"Page":{"pageInfo":{"total":120,"hasNextPage":true},"media":[{"id":1},{"id":2}]}}}`)
	})
	ctx := context.Background()

	season, year := Season(time.Now())
	next, nextYear := NextSeason(time.Now())
	for sort, want := range map[string]string{
		SortTrending:  "sort: TRENDING_DESC)",
		SortPopular:   "sort: POPULARITY_DESC)",
		SortFavourite: "sort: FAVOURITES_DESC)",
		SortTopRated:  "sort: SCORE_DESC, popularity_greater: 5000)",
		SortSeason:    fmt.Sprintf("sort: POPULARITY_DESC, season: %s, seasonYear: %d)", season, year),
		SortUpcoming:  fmt.Sprintf("sort: POPULARITY_DESC, season: %s, seasonYear: %d)", next, nextYear),
	} {
		page, err := c.Discover(ctx, strings.ToUpper(sort), 2, 20)
		if err != nil {
			t.Fatalf("%s: %v", sort, err)
		}
		if !strings.Contains(query, want) {
			t.Errorf("%s asked without %q:\n%s", sort, want, query)
		}
		if !strings.Contains(query, "isAdult: false") {
			t.Errorf("%s did not keep adult titles out", sort)
		}
		if len(page.Media) != 2 || page.Total != 120 || !page.HasNextPage {
			t.Errorf("%s: page = %+v", sort, page)
		}
		if vars["page"] != float64(2) || vars["perPage"] != float64(20) {
			t.Errorf("%s: asked for %v", sort, vars)
		}
	}
}

func TestDiscoverRefusesUnknownSortsAndClampsThePage(t *testing.T) {
	var vars map[string]any
	calls := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Variables map[string]any `json:"variables"`
		}
		decode(t, r, &req)
		vars = req.Variables
		calls++
		io.WriteString(w, `{"data":{"Page":{"pageInfo":{"total":0},"media":[]}}}`)
	})
	ctx := context.Background()

	if _, err := c.Discover(ctx, "newest", 1, 20); err == nil || calls != 0 {
		t.Fatalf("an unknown sort: err=%v, %d requests sent", err, calls)
	}
	// A computed window is not something AniList can be asked for directly.
	if _, err := c.Discover(ctx, SortWeek, 1, 20); err == nil {
		t.Error("the week window was sent to AniList as a sort")
	}

	if _, err := c.Discover(ctx, SortPopular, 0, 500); err != nil {
		t.Fatal(err)
	}
	if vars["page"] != float64(1) || vars["perPage"] != float64(30) {
		t.Errorf("asked for %v, want page 1 of 30", vars)
	}
}
