package server

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"time"

	"kuro/internal/anilist"
	"kuro/internal/store"
)

type discoverItem struct {
	ID      int     `json:"id"`
	Title   string  `json:"title"`
	Romaji  string  `json:"romaji"`
	English *string `json:"english,omitempty"`
	// Three sizes: a row thumbnail loading the full cover cost tens of MB.
	Cover      *string `json:"cover,omitempty"`
	Thumb      *string `json:"thumb,omitempty"`
	Banner     *string `json:"banner,omitempty"`
	Color      *string `json:"color,omitempty"`
	Format     *string `json:"format,omitempty"`
	Status     *string `json:"status,omitempty"`
	Episodes   *int    `json:"episodes,omitempty"`
	Season     *string `json:"season,omitempty"`
	SeasonYear *int    `json:"seasonYear,omitempty"`
	Score      *int    `json:"score,omitempty"`
	Popularity *int    `json:"popularity,omitempty"`
	Genres     []string
	// Carried so a card can answer "what is this" without opening the show.
	Description *string `json:"description,omitempty"`
	OnList      bool    `json:"onList"`
	Progress    int     `json:"progress"`
}

// Each window costs ~30s of upstream calls, too long to sit behind a tab click.
func (s *Server) WarmRankings(ctx context.Context) {
	for {
		for _, sort := range []string{anilist.SortWeek, anilist.SortMonth} {
			days, _ := anilist.RisingWindow(sort)
			result, err := s.anilist.Rising(ctx, days, 30)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				s.log.Warn("warm ranking", "sort", sort, "err", err)
				continue
			}
			s.rising.put(sort, result)
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(risingTTL):
		}
	}
}

// The computed windows ignore paging: the list is the whole result.
func (s *Server) ranked(r *http.Request, sort string, page, perPage int) (anilist.DiscoverPage, error) {
	days, computed := anilist.RisingWindow(sort)
	if !computed {
		return s.anilist.Discover(r.Context(), sort, page, perPage)
	}

	if hit, ok := s.rising.get(sort); ok {
		return hit, nil
	}
	result, err := s.anilist.Rising(r.Context(), days, max(perPage, 30))
	if err != nil {
		return anilist.DiscoverPage{}, err
	}
	s.rising.put(sort, result)
	return result, nil
}

// The trend data behind these only changes once a day.
type risingCache struct {
	mu   sync.Mutex
	page map[string]risingEntry
}

type risingEntry struct {
	result anilist.DiscoverPage
	at     time.Time
}

const risingTTL = 3 * time.Hour

func (c *risingCache) get(key string) (anilist.DiscoverPage, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	e, ok := c.page[key]
	if !ok || time.Since(e.at) > risingTTL {
		return anilist.DiscoverPage{}, false
	}
	return e.result, true
}

func (c *risingCache) put(key string, result anilist.DiscoverPage) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.page == nil {
		c.page = map[string]risingEntry{}
	}
	c.page[key] = risingEntry{result: result, at: time.Now()}
}

// discover ranks anime by trending, popularity, season or score. Week and month
// are computed from popularity gained inside the window, not AniList orderings.
func (s *Server) discover(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	sort := q.Get("sort")
	if sort == "" {
		sort = anilist.SortTrending
	}
	page, _ := strconv.Atoi(q.Get("page"))
	perPage, _ := strconv.Atoi(q.Get("perPage"))

	result, err := s.ranked(r, sort, page, perPage)
	if err != nil {
		send(w, http.StatusBadRequest, map[string]any{
			"error": err.Error(), "sorts": anilist.Sorts(),
		})
		return
	}

	onList, err := s.store.ListProgress(r.Context())
	if err != nil {
		s.log.Warn("list progress", "err", err)
	}
	titles := s.store.TitleMode(r.Context(), 0)

	items := make([]discoverItem, 0, len(result.Media))
	for _, m := range result.Media {
		progress, listed := onList[m.ID]

		it := discoverItem{
			ID:          m.ID,
			English:     m.Title.English,
			Cover:       m.CoverImage.Large,
			Thumb:       m.CoverImage.Medium,
			Banner:      m.BannerImage,
			Color:       m.CoverImage.Color,
			Format:      m.Format,
			Status:      m.Status,
			Episodes:    m.Episodes,
			Season:      m.Season,
			SeasonYear:  m.SeasonYear,
			Score:       m.AverageScore,
			Popularity:  m.Popularity,
			Genres:      m.Genres,
			Description: m.Description,
			OnList:      listed,
			Progress:    progress,
		}
		if m.Title.Romaji != nil {
			it.Romaji = *m.Title.Romaji
		}
		it.Title = store.PickTitle(titles, it.Romaji, it.English)
		items = append(items, it)
	}

	if page <= 0 {
		page = 1
	}
	season, year := anilist.Season(time.Now())

	send(w, http.StatusOK, map[string]any{
		"items":   items,
		"sort":    sort,
		"page":    page,
		"hasMore": result.HasNextPage,
		"total":   result.Total,
		"sorts":   anilist.Sorts(),
		"season":  map[string]any{"season": season, "year": year},
	})
}
