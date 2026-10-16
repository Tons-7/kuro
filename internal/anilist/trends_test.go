package anilist

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"
)

func media(id, popularity int) Media {
	return Media{ID: id, Popularity: &popularity}
}

func TestRankByGainOrdersByWhatWasAddedInTheWindow(t *testing.T) {
	pool := []Media{
		media(1, 500_000), // huge, barely moved
		media(2, 40_000),  // small, moved a lot
		media(3, 90_000),
	}
	baseline := map[int]int{1: 499_000, 2: 25_000, 3: 88_000}

	got := rankByGain(pool, baseline)

	want := []int{2, 3, 1}
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d", len(got), len(want))
	}
	for i, id := range want {
		if got[i].ID != id {
			t.Fatalf("position %d = %d, want %d; a big library is not a big week", i, got[i].ID, id)
		}
	}
}

// A title with no reading at the window start is dropped, not credited its whole count.
func TestRankByGainDropsUnmeasurableTitles(t *testing.T) {
	pool := []Media{
		media(1, 61_000), // announced mid-window, no baseline
		media(2, 470_000),
	}
	baseline := map[int]int{2: 467_000}

	got := rankByGain(pool, baseline)

	if len(got) != 1 || got[0].ID != 2 {
		t.Fatalf("got %+v, want only the title with a baseline", ids(got))
	}
}

func TestRankByGainSkipsMediaWithoutPopularity(t *testing.T) {
	pool := []Media{{ID: 1}, media(2, 100)}
	got := rankByGain(pool, map[int]int{1: 10, 2: 50})

	if len(got) != 1 || got[0].ID != 2 {
		t.Fatalf("got %v, want only the title carrying a count", ids(got))
	}
}

func ids(list []Media) []int {
	out := make([]int, len(list))
	for i, m := range list {
		out[i] = m.ID
	}
	return out
}

// A batch is one request: a title missing the first day is dropped, not a reason to page on.
func TestPopularityAtReadsOnlyTheWindowsFirstDay(t *testing.T) {
	day := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	first, fourth := day.Unix(), day.AddDate(0, 0, 3).Unix()
	requests := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		io.WriteString(w, fmt.Sprintf(`{"data":{"Page":{"pageInfo":{"hasNextPage":true},"mediaTrends":[
			{"mediaId":1,"date":%d,"popularity":100},
			{"mediaId":1,"date":%d,"popularity":130},
			{"mediaId":2,"date":%d,"popularity":60}]}}}`, first, first+86400, fourth))
	})

	got, err := c.popularityAt(context.Background(), []int{1, 2}, day)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[1] != 100 {
		t.Fatalf("baseline = %v, want only title 1 at its first-day reading", got)
	}
	if requests != 1 {
		t.Fatalf("%d requests; the page already ran past the first day", requests)
	}
}

// Paging goes on only while the first day's rows may continue on the next page.
func TestPopularityAtPagesWhileStillOnTheFirstDay(t *testing.T) {
	day := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	first := day.Unix()
	var pages []float64
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Variables map[string]any `json:"variables"`
		}
		decode(t, r, &req)
		page := req.Variables["page"].(float64)
		pages = append(pages, page)
		if page == 1 {
			io.WriteString(w, fmt.Sprintf(`{"data":{"Page":{"pageInfo":{"hasNextPage":true},"mediaTrends":[
				{"mediaId":1,"date":%d,"popularity":100}]}}}`, first))
			return
		}
		io.WriteString(w, fmt.Sprintf(`{"data":{"Page":{"pageInfo":{"hasNextPage":false},"mediaTrends":[
			{"mediaId":2,"date":%d,"popularity":50}]}}}`, first+3600))
	})

	got, err := c.popularityAt(context.Background(), []int{1, 2}, day)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 2 || got[1] != 100 || got[2] != 50 {
		t.Fatalf("pages %v, baseline %v; both first-day readings should be read", pages, got)
	}
}
