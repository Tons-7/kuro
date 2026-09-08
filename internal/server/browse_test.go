package server

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"kuro/internal/config"
)

// The Browse page puts its filters in the query string, so the sort the user
// picked has to survive the whole way to the AniList request.
func TestBrowseSortReachesAniList(t *testing.T) {
	for _, tc := range []struct {
		name   string
		target string
		search any
		want   string
	}{
		{"search alone", "/api/browse?q=conan&formats=MOVIE", "conan", "SEARCH_MATCH"},
		{"search sorted by score", "/api/browse?q=conan&formats=MOVIE&sort=score", "conan", "SCORE_DESC"},
		{"search sorted by newest", "/api/browse?q=conan&formats=MOVIE&sort=newest", "conan", "START_DATE_DESC"},
		{"sort without a search", "/api/browse?formats=MOVIE&sort=score", nil, "SCORE_DESC"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var vars map[string]any
			h := newHarness(t, config.Config{}, func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					Variables map[string]any `json:"variables"`
				}
				json.NewDecoder(r.Body).Decode(&req)
				vars = req.Variables
				io.WriteString(w, `{"data":{"Page":{"pageInfo":{},"media":[]}}}`)
			})

			if res, _ := h.do(t, http.MethodGet, tc.target); res.StatusCode != http.StatusOK {
				t.Fatalf("status %d", res.StatusCode)
			}
			list, _ := vars["sort"].([]any)
			if len(list) != 1 || list[0] != tc.want {
				t.Errorf("sort = %#v, want %s", vars["sort"], tc.want)
			}
			if vars["search"] != tc.search {
				t.Errorf("search = %v, want %v", vars["search"], tc.search)
			}
			if formats, _ := vars["formats"].([]any); len(formats) != 1 || formats[0] != "MOVIE" {
				t.Errorf("the format filter was lost: %#v", vars["formats"])
			}
		})
	}
}
