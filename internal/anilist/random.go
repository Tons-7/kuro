package anilist

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"
	"time"
)

// popularityFloor keeps a uniform draw off forgotten OVAs.
const popularityFloor = 5000

// randomQuery asks for one anime at a random offset; pageInfo.total bounds the next draw.
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
	by map[string]total
}

type total struct {
	n  int
	at time.Time
}

// How long a filter nobody matches is answered without a request; a repeated click costs nothing.
const noMatchFor = 10 * time.Minute

var errNoMatch = fmt.Errorf("anilist: no anime matched")

// get is the filter's last count; known is false for one never seen, or an empty one seen a while ago.
func (t *totals) get(key string) (n int, known bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	c, ok := t.by[key]
	if !ok || (c.n == 0 && time.Since(c.at) > noMatchFor) {
		return 0, false
	}
	return c.n, true
}

func (t *totals) set(key string, n int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.by == nil {
		t.by = map[string]total{}
	}
	t.by[key] = total{n: n, at: time.Now()}
}

// Random returns one anime matching format, every genre and year; adult only with the Hentai genre.
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
	bound, known := c.totals.get(key)
	if known && bound == 0 {
		return Media{}, errNoMatch
	}
	if !known {
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

		total := out.Page.PageInfo.Total
		c.totals.set(key, total)
		if total == 0 {
			break
		}
		bound = total
		if len(out.Page.Media) > 0 {
			return out.Page.Media[0], nil
		}
		// An overshoot returns an empty page; the bound is now known, so the next attempt lands.
		if attempt == 2 {
			break
		}
	}
	return Media{}, errNoMatch
}
