package server

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"kuro/internal/anilist"
	"kuro/internal/store"
)

func csv(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// browse is search with the full filter set: genres, tags, year, season,
// format, status, score and episode count.
func (s *Server) browse(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	num := func(key string) int { n, _ := strconv.Atoi(q.Get(key)); return n }

	result, err := s.anilist.BrowseMedia(r.Context(), anilist.Browse{
		Search:        q.Get("q"),
		Genres:        csv(q.Get("genres")),
		ExcludeGenres: csv(q.Get("excludeGenres")),
		Tags:          csv(q.Get("tags")),
		Year:          num("year"),
		Season:        q.Get("season"),
		Formats:       csv(q.Get("formats")),
		Statuses:      csv(q.Get("statuses")),
		MinScore:      num("minScore"),
		MinEpisodes:   num("minEpisodes"),
		MaxEpisodes:   num("maxEpisodes"),
		Country:       q.Get("country"),
		Source:        q.Get("source"),
		Sort:          q.Get("sort"),
		Page:          num("page"),
		PerPage:       num("perPage"),
	})
	if err != nil {
		s.fail(w, "browse", err)
		return
	}

	page := max(num("page"), 1)
	send(w, http.StatusOK, map[string]any{
		"items":   s.decorate(r, result.Media),
		"page":    page,
		"hasMore": result.HasNextPage,
		"total":   result.Total,
	})
}

// decorate turns AniList media into cards: resolved title plus whether it is
// already on the user's list, which every grid in the UI needs.
func (s *Server) decorate(r *http.Request, media []anilist.Media) []discoverItem {
	onList, err := s.store.ListProgress(r.Context())
	if err != nil {
		s.log.Warn("list progress", "err", err)
	}
	titles := s.store.TitleMode(r.Context(), 0)

	items := make([]discoverItem, 0, len(media))
	for _, m := range media {
		progress, listed := onList[m.ID]

		it := discoverItem{
			ID:         m.ID,
			English:    m.Title.English,
			Cover:      m.CoverImage.Large,
			Thumb:      m.CoverImage.Medium,
			Banner:     m.BannerImage,
			Color:      m.CoverImage.Color,
			Format:     m.Format,
			Status:     m.Status,
			Episodes:   m.Episodes,
			Season:     m.Season,
			SeasonYear: m.SeasonYear,
			Score:      m.AverageScore,
			Popularity: m.Popularity,
			Genres:     m.Genres,
			OnList:     listed,
			Progress:   progress,
		}
		if m.Title.Romaji != nil {
			it.Romaji = *m.Title.Romaji
		}
		it.Title = store.PickTitle(titles, it.Romaji, it.English)
		items = append(items, it)
	}
	return items
}

// recommend is "more like this". AniList's recommendations are community
// submitted, so a show with none falls back to a genre match.
func (s *Server) recommend(w http.ResponseWriter, r *http.Request) {
	animeID, _ := strconv.Atoi(r.URL.Query().Get("anime"))
	if animeID <= 0 {
		send(w, http.StatusBadRequest, map[string]any{"error": "anime is required"})
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))

	rec, err := s.anilist.Recommend(r.Context(), animeID, limit)
	if err != nil {
		s.fail(w, "recommend", err)
		return
	}

	media := make([]anilist.Media, 0, len(rec.Items))
	for _, item := range rec.Items {
		media = append(media, item.Media)
	}
	source := "community"

	if len(media) == 0 && len(rec.Genres) > 0 {
		source = "genre"
		fallback, err := s.anilist.BrowseMedia(r.Context(), anilist.Browse{
			Genres: rec.Genres, Sort: "popular", PerPage: 20,
		})
		if err != nil {
			s.fail(w, "recommend fallback", err)
			return
		}
		for _, m := range fallback.Media {
			if m.ID != animeID {
				media = append(media, m)
			}
		}
	}

	send(w, http.StatusOK, map[string]any{
		"items":  s.decorate(r, media),
		"source": source,
		"genres": rec.Genres,
	})
}

// vocabulary is fixed for weeks at a time, so it is cached rather than spending
// a request from a 30-per-minute budget every time a filter panel opens.
type vocabulary struct {
	mu      sync.Mutex
	genres  []string
	tags    []anilist.TagOption
	fetched time.Time
}

func (s *Server) filters(w http.ResponseWriter, r *http.Request) {
	s.vocab.mu.Lock()
	stale := time.Since(s.vocab.fetched) > 24*time.Hour || len(s.vocab.genres) == 0
	genres, tags := s.vocab.genres, s.vocab.tags
	s.vocab.mu.Unlock()

	if stale {
		fresh, freshTags, err := s.anilist.Vocabulary(r.Context())
		if err != nil {
			// A cached copy beats an error page on the filter panel.
			if len(genres) == 0 {
				s.fail(w, "filters", err)
				return
			}
		} else {
			genres, tags = fresh, freshTags
			s.vocab.mu.Lock()
			s.vocab.genres, s.vocab.tags, s.vocab.fetched = fresh, freshTags, time.Now()
			s.vocab.mu.Unlock()
		}
	}

	season, year := anilist.Season(time.Now())
	years := make([]int, 0, year-1939)
	for y := year + 1; y >= 1940; y-- {
		years = append(years, y)
	}

	send(w, http.StatusOK, map[string]any{
		"genres":   genres,
		"tags":     tags,
		"formats":  anilist.Formats,
		"statuses": anilist.Statuses,
		"seasons":  anilist.Seasons,
		"sources":  anilist.Sources,
		"sorts":    anilist.BrowseSorts(),
		"years":    years,
		"current":  map[string]any{"season": season, "year": year},
	})
}
