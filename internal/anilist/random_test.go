package anilist

import (
	"context"
	"io"
	"net/http"
	"testing"
)

// A guessed page past the real total returns an empty page with the total;
// the next attempt must land inside it rather than give up.
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
