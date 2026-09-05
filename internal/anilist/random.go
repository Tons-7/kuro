package anilist

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"
)

// A uniform draw across every anime almost always lands on a forgotten OVA;
// popularityFloor keeps the result to things worth watching.
const popularityFloor = 5000

// randomQuery asks for one anime at a random offset; pageInfo.total bounds the
// next draw.
const randomQuery = `query Random($page: Int!, $format: MediaFormat, $genres: [String], $year: Int, $isAdult: Boolean, $floor: Int) {
  Page(page: $page, perPage: 1) {
    pageInfo { total }
    media(type: ANIME, isAdult: $isAdult, popularity_greater: $floor, format: $format, genre_in: $genres, seasonYear: $year, sort: POPULARITY_DESC) {` + mediaFields + `}
  }
}`

// totals caches how many anime match each filter so a draw is one request, not
// two. Slight staleness only skews the distribution, never the result.
type totals struct {
	mu sync.Mutex
	by map[string]int
}

func (t *totals) get(key string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	if n, ok := t.by[key]; ok && n > 0 {
		return n
	}
	return 0
}

func (t *totals) set(key string, n int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.by == nil {
		t.by = map[string]int{}
	}
	t.by[key] = n
}

// Random returns one anime matching a format, every genre given, and a year.
// Adult titles are excluded unless Hentai is a chosen genre.
func (c *Client) Random(ctx context.Context, format string, genres []string, year int) (Media, error) {
	vars := map[string]any{
		"floor":   popularityFloor,
		"isAdult": hasGenre(genres, "Hentai"),
	}
	setString(vars, "format", strings.ToUpper(format))
	setList(vars, "genres", genres)
	if year > 0 {
		vars["year"] = year
	}
	// Hentai never reaches the popularity floor.
	if hasGenre(genres, "Hentai") {
		vars["floor"] = 0
	}
	key := fmt.Sprint(format, "|", strings.Join(genres, ","), "|", year)

	// The first draw for an unseen filter is a guess; the response corrects it.
	bound := c.totals.get(key)
	if bound <= 0 {
		bound = 500
	}

	for attempt := range 3 {
		var out struct {
			Page struct {
				PageInfo struct {
					Total int `json:"total"`
				} `json:"pageInfo"`
				Media []Media `json:"media"`
			} `json:"Page"`
		}

		vars["page"] = rand.IntN(bound) + 1
		if err := c.Query(ctx, randomQuery, vars, &out); err != nil {
			return Media{}, err
		}

		if total := out.Page.PageInfo.Total; total > 0 {
			c.totals.set(key, total)
			bound = total
		}
		if len(out.Page.Media) > 0 {
			return out.Page.Media[0], nil
		}
		// An overshoot past the real total returns an empty page; the bound is
		// now known, so the next attempt lands.
		if attempt == 2 || bound <= 0 {
			break
		}
	}
	return Media{}, fmt.Errorf("anilist: no anime matched")
}
