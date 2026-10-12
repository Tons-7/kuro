package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"kuro/internal/config"
)

// A studio of five anime and a manga, two pages: the first has the TV series, the film and the manga.
const studioPage1 = `
  {"id":1,"title":{"romaji":"Show A","english":"Show A English"},"synonyms":["Alpha"],"format":"TV","status":"FINISHED",
   "genres":["Action"],"season":"FALL","seasonYear":2022,"startDate":{"year":2022,"month":10,"day":12},"averageScore":86,
   "popularity":500,"favourites":90,"trending":5,"episodes":12,"countryOfOrigin":"JP","source":"MANGA","type":"ANIME",
   "studios":{"nodes":[{"id":569,"name":"MAPPA"}]}},
  {"id":2,"title":{"romaji":"Film B"},"format":"MOVIE","status":"FINISHED","genres":["Action","Drama"],
   "startDate":{"year":2023,"month":3,"day":1},"averageScore":90,"popularity":300,"favourites":120,"trending":50,
   "episodes":1,"countryOfOrigin":"JP","source":"ORIGINAL","type":"ANIME"},
  {"id":3,"title":{"romaji":"Manga C"},"format":"MANGA","status":"FINISHED","genres":["Action"],"type":"MANGA"}`

const studioPage2 = `
  {"id":4,"title":{"romaji":"Show D"},"format":"TV","status":"RELEASING","genres":["Action","Drama"],"season":"WINTER",
   "seasonYear":2024,"startDate":{"year":2024,"month":1,"day":5},"averageScore":70,"popularity":800,"favourites":30,
   "trending":100,"countryOfOrigin":"JP","source":"LIGHT_NOVEL","type":"ANIME"},
  {"id":5,"title":{"romaji":"Short E","native":"イー"},"format":"TV_SHORT","status":"FINISHED","genres":["Comedy"],
   "season":"SPRING","seasonYear":2022,"startDate":{"year":2022,"month":4,"day":1},"popularity":50,"favourites":2,
   "trending":0,"episodes":24,"countryOfOrigin":"CN","type":"ANIME"},
  {"id":6,"title":{"romaji":"Zeta OVA"},"format":"OVA","status":"FINISHED","genres":["Action"],"averageScore":60,
   "popularity":100,"favourites":10,"trending":1,"episodes":2,"countryOfOrigin":"JP","type":"ANIME"}`

// studioHarness serves the studio above, two pages, and records every studio query's variables.
func studioHarness(t *testing.T) (*harness, func() []map[string]any) {
	t.Helper()
	return studioHarnessOf(t, func(page int) (string, bool) {
		if page == 1 {
			return studioPage1, true
		}
		return studioPage2, false
	})
}

func studioHarnessOf(t *testing.T, pageOf func(page int) (nodes string, more bool)) (*harness, func() []map[string]any) {
	t.Helper()
	var mu sync.Mutex
	var asked []map[string]any
	h := newHarness(t, config.Config{}, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		json.Unmarshal(body, &req)
		if !strings.Contains(req.Query, "StudioMedia") {
			io.WriteString(w, `{"data":{}}`)
			return
		}
		page := int(req.Variables["page"].(float64))
		mu.Lock()
		asked = append(asked, req.Variables)
		mu.Unlock()
		nodes, more := pageOf(page)
		fmt.Fprintf(w, `{"data":{"Studio":{"name":"MAPPA","media":{"pageInfo":{"total":6,"hasNextPage":%v},"nodes":[%s]}}}}`, more, nodes)
	})
	return h, func() []map[string]any { mu.Lock(); defer mu.Unlock(); return append([]map[string]any(nil), asked...) }
}

func browseIDs(t *testing.T, h *harness, target string) ([]int, map[string]any) {
	t.Helper()
	res, body := h.do(t, "GET", target)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("%s: status %d: %v", target, res.StatusCode, body)
	}
	items, _ := body["items"].([]any)
	ids := make([]int, 0, len(items))
	for _, it := range items {
		ids = append(ids, int(it.(map[string]any)["id"].(float64)))
	}
	return ids, body
}

// A studio's works come from the studio; the filters narrow the whole list, most popular first.
func TestBrowseByStudio(t *testing.T) {
	h, _ := studioHarness(t)

	ids, body := browseIDs(t, h, "/api/browse?studio=569&formats=TV")
	if fmt.Sprint(ids) != "[4 1]" {
		t.Fatalf("items = %v, want the two TV series by popularity", ids)
	}
	card := body["items"].([]any)[1].(map[string]any)
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

// Every browse filter narrows a studio's list the way the catalogue reads it, and they stack.
func TestStudioFiltersNarrowTogether(t *testing.T) {
	h, _ := studioHarness(t)
	// The whole list first, so even the unfiltered page below is answered from it.
	browseIDs(t, h, "/api/browse?studio=569&genres=Action")
	for _, tt := range []struct{ query, want string }{
		{"", "[4 1 2 6 5]"},
		{"&genres=Action", "[4 1 2 6]"},
		{"&genres=Action,Drama", "[4 2]"},
		{"&excludeGenres=Drama", "[1 6 5]"},
		{"&statuses=FINISHED", "[1 2 6 5]"},
		{"&formats=TV,MOVIE", "[4 1 2]"},
		{"&year=2022", "[1 5]"},
		{"&year=2023", "[2]"}, // no season on the film: its start year counts
		{"&season=FALL", "[1]"},
		{"&year=2022&season=SPRING", "[5]"},
		{"&q=show", "[4 1]"},
		{"&q=ALPHA", "[1]"}, // a synonym, case aside
		{"&q=" + "%E3%82%A4%E3%83%BC", "[5]"},
		{"&q=a+english", "[1]"}, // every word
		{"&q=nothing+like+this", "[]"},
		{"&minScore=85", "[1 2]"},
		{"&minEpisodes=12", "[1 5]"},
		{"&maxEpisodes=2", "[2 6]"},
		{"&country=CN", "[5]"},
		{"&source=original", "[2]"},
		{"&formats=TV&statuses=FINISHED&genres=Action&year=2022&season=FALL&q=show", "[1]"},
		{"&formats=MOVIE&statuses=RELEASING", "[]"},
	} {
		if ids, _ := browseIDs(t, h, "/api/browse?studio=569"+tt.query); fmt.Sprint(ids) != tt.want {
			t.Errorf("%q: items %v, want %v", tt.query, ids, tt.want)
		}
	}
}

// Every sort the browse offers orders the list as the catalogue would; unknown values go last.
func TestStudioSortsTheWholeList(t *testing.T) {
	h, _ := studioHarness(t)
	for _, tt := range []struct{ sort, want string }{
		{"popular", "[4 1 2 6 5]"},
		{"score", "[2 1 4 6 5]"},
		{"favourites", "[2 1 4 6 5]"},
		{"trending", "[4 2 1 6 5]"},
		{"episodes", "[5 1 6 2 4]"},
		{"newest", "[4 2 1 5 6]"},
		{"oldest", "[5 1 2 4 6]"},
		{"title", "[2 5 1 4 6]"},
		{"no-such-sort", "[4 1 2 6 5]"},
	} {
		// A filter that keeps everything, so the answer is the kept list rather than the catalogue's first page.
		if ids, _ := browseIDs(t, h, "/api/browse?studio=569&excludeGenres=Mecha&sort="+tt.sort); fmt.Sprint(ids) != tt.want {
			t.Errorf("sort %s: items %v, want %v", tt.sort, ids, tt.want)
		}
	}
}

// The count is exact and the pages come from the list: a studio of 65 works, 30 to a page.
func TestStudioTotalIsExactAndPaged(t *testing.T) {
	h, _ := studioHarnessOf(t, func(page int) (string, bool) {
		var nodes []string
		for i := (page-1)*50 + 1; i <= min(page*50, 65); i++ {
			nodes = append(nodes, fmt.Sprintf(`{"id":%d,"title":{"romaji":"Work %d"},"format":"TV","genres":["Action"],"popularity":%d,"type":"ANIME"}`, i, i, 1000-i))
		}
		return strings.Join(nodes, ","), page*50 < 65
	})
	for _, tt := range []struct {
		page, items int
		more        bool
	}{{1, 30, true}, {2, 30, true}, {3, 5, false}, {4, 0, false}} {
		ids, body := browseIDs(t, h, fmt.Sprintf("/api/browse?studio=7&genres=Action&page=%d", tt.page))
		if len(ids) != tt.items || body["hasMore"] != tt.more || body["total"] != 65.0 || body["page"] != float64(tt.page) {
			t.Errorf("page %d: %d items, more %v, total %v, page %v", tt.page, len(ids), body["hasMore"], body["total"], body["page"])
		}
		if tt.items > 0 && ids[0] != (tt.page-1)*30+1 {
			t.Errorf("page %d starts at %d", tt.page, ids[0])
		}
	}
}

// The list is fetched once (every page) and kept; later filters, sorts and pages ask nothing more.
func TestStudioListIsFetchedOnceAndKept(t *testing.T) {
	h, asked := studioHarness(t)
	browseIDs(t, h, "/api/browse?studio=569&genres=Action")
	browseIDs(t, h, "/api/browse?studio=569&year=2022&sort=title")
	browseIDs(t, h, "/api/browse?studio=569&page=2")
	if n := len(asked()); n != 2 {
		t.Fatalf("%d studio queries, want one per page", n)
	}
}

// The first unfiltered page comes from the catalogue at once, in the sort asked; the list loads behind it.
func TestStudioFirstPageIsAnsweredBeforeTheListIsWhole(t *testing.T) {
	h, asked := studioHarness(t)
	ids, body := browseIDs(t, h, "/api/browse?studio=569&sort=score")
	if fmt.Sprint(ids) != "[1 2]" || body["total"] != 6.0 || body["hasMore"] != true {
		t.Fatalf("first page: items %v, total %v, more %v; want the catalogue's own first page", ids, body["total"], body["hasMore"])
	}
	first := asked()
	if len(first) == 0 || fmt.Sprint(first[0]["sort"]) != "[SCORE_DESC]" {
		t.Errorf("the catalogue was asked %v, want the sort chosen", first)
	}

	deadline := time.Now().Add(5 * time.Second)
	for len(asked()) < 3 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if n := len(asked()); n != 3 {
		t.Fatalf("%d studio queries, want the first page and the two of the whole list", n)
	}
	if ids, _ := browseIDs(t, h, "/api/browse?studio=569&sort=score&genres=Action"); fmt.Sprint(ids) != "[2 1 4 6]" {
		t.Errorf("filtered after the fill: %v", ids)
	}
	if n := len(asked()); n != 3 {
		t.Errorf("%d studio queries after a filtered request, want no more", n)
	}
}
