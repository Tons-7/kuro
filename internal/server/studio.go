package server

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"kuro/internal/anilist"
)

// The catalogue cannot filter a studio's works: the list is fetched once, kept, and narrowed and paged here.
type studioLists struct {
	mu      sync.Mutex
	done    map[int]studioList
	running map[int]chan struct{}
	// first: what the catalogue's first page held per studio and sort, where later pages start.
	first map[int]firstPage
}

type firstPage struct {
	sort string
	n    int
}

func (c *studioLists) servedFirst(id int, sort string, n int) {
	c.mu.Lock()
	if c.first == nil {
		c.first = map[int]firstPage{}
	}
	c.first[id] = firstPage{sort: sort, n: n}
	c.mu.Unlock()
}

// shortfall is how many works fewer than perPage the first page showed, for the same sort.
func (c *studioLists) shortfall(id int, sort string, perPage int) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if f, ok := c.first[id]; ok && f.sort == sort && f.n < perPage {
		return perPage - f.n
	}
	return 0
}

type studioList struct {
	name  string
	works []anilist.Media
	at    time.Time
}

const (
	studioTTL  = 6 * time.Hour
	studioKeep = 24 // lists kept at once; the oldest goes
)

// get answers from the kept list or fetches it, one fetch for all askers; a list from saved pages is not kept.
func (c *studioLists) get(ctx context.Context, id int, fetch func() (string, []anilist.Media, error)) (studioList, error) {
	for {
		c.mu.Lock()
		if l, ok := c.done[id]; ok && time.Since(l.at) < studioTTL {
			c.mu.Unlock()
			return l, nil
		}
		wait, busy := c.running[id]
		if !busy {
			if c.running == nil {
				c.running, c.done = map[int]chan struct{}{}, map[int]studioList{}
			}
			wait = make(chan struct{})
			c.running[id] = wait
		}
		c.mu.Unlock()
		if busy {
			select {
			case <-wait:
				continue
			case <-ctx.Done():
				return studioList{}, ctx.Err()
			}
		}

		name, works, err := fetch()
		l := studioList{name: name, works: works, at: time.Now()}
		c.mu.Lock()
		delete(c.running, id)
		if err == nil && !anilist.UsedSaved(ctx) {
			c.done[id] = l
			c.evict()
		}
		c.mu.Unlock()
		close(wait)
		return l, err
	}
}

func (c *studioLists) have(id int) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	l, ok := c.done[id]
	return ok && time.Since(l.at) < studioTTL
}

func (c *studioLists) evict() {
	for len(c.done) > studioKeep {
		oldest, at := 0, time.Time{}
		for id, l := range c.done {
			if at.IsZero() || l.at.Before(at) {
				oldest, at = id, l.at
			}
		}
		delete(c.done, oldest)
	}
}

// browseStudio lists a studio's works, narrowed by every browse filter, sorted, with an exact count; the
// first unfiltered page comes straight from the catalogue while the whole list (up to 20 pages) loads.
func (s *Server) browseStudio(w http.ResponseWriter, r *http.Request, studio int) {
	b := browseFilter(r.URL.Query())
	page, perPage := max(b.Page, 1), perPageOf(b.PerPage)

	if page == 1 && !narrows(b) && !s.studioWorks.have(studio) {
		name, result, err := s.anilist.StudioMedia(r.Context(), studio, b.Sort, 1, perPage)
		if err != nil {
			s.fail(w, "studio", err)
			return
		}
		go s.fillStudio(studio)
		// The total counts the manga the page dropped; page 2 starts where this one ended.
		s.studioWorks.servedFirst(studio, b.Sort, len(result.Media))
		send(w, http.StatusOK, map[string]any{
			"items": s.decorate(r, result.Media), "page": 1, "hasMore": result.HasNextPage, "total": result.Total - result.Dropped,
			"studio": map[string]any{"id": studio, "name": name},
		})
		return
	}

	list, err := s.studioWorks.get(r.Context(), studio, func() (string, []anilist.Media, error) {
		return s.anilist.StudioWorks(r.Context(), studio)
	})
	if err != nil {
		s.fail(w, "studio", err)
		return
	}
	works := narrowMedia(list.works, b)
	sortMedia(works, b.Sort)
	total := len(works)
	from := (page - 1) * perPage
	if page > 1 && !narrows(b) {
		from -= s.studioWorks.shortfall(studio, b.Sort, perPage)
	}
	from = min(max(from, 0), total)
	to := min(from+perPage, total)
	send(w, http.StatusOK, map[string]any{
		"items": s.decorate(r, works[from:to]), "page": page, "hasMore": to < total, "total": total,
		"studio": map[string]any{"id": studio, "name": list.name},
	})
}

// fillStudio loads the whole list in the background, standing aside for clicks on the budget.
func (s *Server) fillStudio(studio int) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	ctx, _ = anilist.WithServed(anilist.Background(ctx))
	if _, err := s.studioWorks.get(ctx, studio, func() (string, []anilist.Media, error) {
		return s.anilist.StudioWorks(ctx, studio)
	}); err != nil {
		s.log.Warn("studio list", "studio", studio, "err", err)
	}
}

// perPageOf is the catalogue's own range, so a studio page is the size any other browse page is.
func perPageOf(n int) int {
	if n <= 0 {
		return 30
	}
	return min(n, 50)
}

// narrows: any filter beyond the sort is set.
func narrows(b anilist.Browse) bool {
	return b.Search != "" || len(b.Genres)+len(b.ExcludeGenres)+len(b.Tags)+len(b.Formats)+len(b.Statuses) > 0 ||
		b.Year > 0 || b.Season != "" || b.MinScore > 0 || b.MinEpisodes > 0 || b.MaxEpisodes > 0 ||
		b.Country != "" || b.Source != ""
}

// narrowMedia reads the filters as the catalogue does: every genre and tag named, score strictly above,
// episode bounds inclusive, adult titles only with Hentai.
func narrowMedia(works []anilist.Media, b anilist.Browse) []anilist.Media {
	terms := strings.Fields(strings.ToLower(b.Search))
	adult := containsFold(b.Genres, "Hentai")
	var out []anilist.Media
	for _, m := range works {
		switch {
		case m.IsAdult != adult,
			len(terms) > 0 && !titleHas(m, terms),
			!hasAll(m.Genres, b.Genres), hasAny(m.Genres, b.ExcludeGenres), !hasAll(tagNames(m), b.Tags),
			len(b.Formats) > 0 && !containsFold(b.Formats, deref(m.Format)),
			len(b.Statuses) > 0 && !containsFold(b.Statuses, deref(m.Status)),
			b.Year > 0 && yearOf(m) != b.Year,
			b.Season != "" && !strings.EqualFold(deref(m.Season), b.Season),
			b.MinScore > 0 && (m.AverageScore == nil || *m.AverageScore <= b.MinScore),
			b.MinEpisodes > 0 && (m.Episodes == nil || *m.Episodes < b.MinEpisodes),
			b.MaxEpisodes > 0 && (m.Episodes == nil || *m.Episodes > b.MaxEpisodes),
			b.Country != "" && !strings.EqualFold(deref(m.CountryOfOrigin), b.Country),
			b.Source != "" && !strings.EqualFold(deref(m.Source), b.Source):
			continue
		}
		out = append(out, m)
	}
	return out
}

// titleHas: every word of the search is in one of the titles or synonyms, case aside.
func titleHas(m anilist.Media, terms []string) bool {
	names := append([]string{deref(m.Title.Romaji), deref(m.Title.English), deref(m.Title.Native), deref(m.Title.UserPreferred)}, m.Synonyms...)
	haystack := strings.ToLower(strings.Join(names, "\n"))
	for _, t := range terms {
		if !strings.Contains(haystack, t) {
			return false
		}
	}
	return true
}

// yearOf is the season's year, or the start year for a work the catalogue gives no season.
func yearOf(m anilist.Media) int {
	if m.SeasonYear != nil {
		return *m.SeasonYear
	}
	if m.StartDate.Year != nil {
		return *m.StartDate.Year
	}
	return 0
}

func tagNames(m anilist.Media) []string {
	out := make([]string, 0, len(m.Tags))
	for _, t := range m.Tags {
		out = append(out, t.Name)
	}
	return out
}

func hasAny(have, want []string) bool {
	for _, w := range want {
		if containsFold(have, w) {
			return true
		}
	}
	return false
}

// sortMedia orders as the catalogue would for the same sort name, unknown values last and ties by id.
func sortMedia(works []anilist.Media, sort string) {
	num := func(p *int) int {
		if p == nil {
			return -1
		}
		return *p
	}
	date := func(m anilist.Media) int {
		d := m.StartDate
		if d.Year == nil {
			return 0
		}
		y, mo, day := *d.Year, 0, 0
		if d.Month != nil {
			mo = *d.Month
		}
		if d.Day != nil {
			day = *d.Day
		}
		return y*10000 + mo*100 + day
	}
	var less func(a, b anilist.Media) int
	desc := func(key func(anilist.Media) int) func(a, b anilist.Media) int {
		return func(a, b anilist.Media) int { return key(b) - key(a) }
	}
	switch strings.ToLower(sort) {
	case "trending":
		less = desc(func(m anilist.Media) int { return num(m.Trending) })
	case "score":
		less = desc(func(m anilist.Media) int { return num(m.AverageScore) })
	case "favourites":
		less = desc(func(m anilist.Media) int { return num(m.Favourites) })
	case "episodes":
		less = desc(func(m anilist.Media) int { return num(m.Episodes) })
	case "newest":
		less = desc(date)
	case "oldest":
		less = func(a, b anilist.Media) int {
			// A work with no date sorts after the dated ones, whichever way the dates go.
			da, db := date(a), date(b)
			if (da == 0) != (db == 0) {
				return db - da
			}
			return da - db
		}
	case "title":
		less = func(a, b anilist.Media) int {
			return strings.Compare(strings.ToLower(deref(a.Title.Romaji)), strings.ToLower(deref(b.Title.Romaji)))
		}
	default:
		less = desc(func(m anilist.Media) int { return num(m.Popularity) })
	}
	slices.SortStableFunc(works, func(a, b anilist.Media) int {
		if c := less(a, b); c != 0 {
			return c
		}
		return a.ID - b.ID
	})
}
