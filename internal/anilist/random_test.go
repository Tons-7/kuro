package anilist

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"
)

// A guess past the real total comes back empty with the total; the next attempt lands inside it.
func TestRandomRetriesInsideTheLearnedTotal(t *testing.T) {
	var pages []float64
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Variables map[string]any `json:"variables"`
		}
		decode(t, r, &req)
		page := req.Variables["page"].(float64)
		pages = append(pages, page)
		if page > 2 {
			io.WriteString(w, `{"data":{"Page":{"pageInfo":{"total":2},"media":[]}}}`)
			return
		}
		io.WriteString(w, `{"data":{"Page":{"pageInfo":{"total":2},"media":[{"id":7}]}}}`)
	})

	for i := range 20 {
		// Force an overshooting first guess every time.
		c.totals.set("|Rare|0", 400)
		m, err := c.Random(context.Background(), "", []string{"Rare"}, 0)
		if err != nil {
			t.Fatalf("attempt %d: %v (pages %v)", i, err, pages)
		}
		if m.ID != 7 {
			t.Fatalf("media = %+v", m)
		}
	}
	if last := pages[len(pages)-1]; last > 2 {
		t.Fatalf("final page %v outside the learned total", last)
	}
}

// A filter nobody matches is answered by its first response, and a repeated click asks nothing for a while.
func TestRandomStopsAtAnEmptyTotal(t *testing.T) {
	requests := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		io.WriteString(w, `{"data":{"Page":{"pageInfo":{"total":0},"media":[]}}}`)
	})

	for range 3 {
		if _, err := c.Random(context.Background(), "MOVIE", []string{"Mecha"}, 2026); err != errNoMatch {
			t.Fatalf("err = %v, want errNoMatch", err)
		}
	}
	if requests != 1 {
		t.Fatalf("%d requests for a filter the first answer already emptied", requests)
	}

	// Once the memory of the empty answer has aged, the filter is asked again.
	c.totals.by["MOVIE|Mecha|2026"] = total{n: 0, at: time.Now().Add(-noMatchFor - time.Second)}
	c.Random(context.Background(), "MOVIE", []string{"Mecha"}, 2026)
	if requests != 2 {
		t.Fatalf("%d requests; an old empty answer should be checked again", requests)
	}
}
