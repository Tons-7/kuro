package server

import (
	"net/http"
	"strconv"
	"time"

	"kuro/internal/store"
)

func (s *Server) jobStatus(w http.ResponseWriter, r *http.Request) {
	if s.jobs == nil {
		send(w, http.StatusServiceUnavailable, map[string]any{"error": "scheduler unavailable"})
		return
	}
	send(w, http.StatusOK, map[string]any{"jobs": s.jobs.Status()})
}

func (s *Server) runJob(w http.ResponseWriter, r *http.Request) {
	if s.jobs == nil {
		send(w, http.StatusServiceUnavailable, map[string]any{"error": "scheduler unavailable"})
		return
	}
	if err := s.jobs.Trigger(r.Context(), r.PathValue("name")); err != nil {
		send(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	send(w, http.StatusOK, map[string]any{"ran": r.PathValue("name")})
}

type scheduleItem struct {
	AnimeID  int     `json:"animeId"`
	Episode  int     `json:"episode"`
	AiringAt int     `json:"airingAt"`
	Title    string  `json:"title"`
	Romaji   string  `json:"romaji"`
	English  *string `json:"english,omitempty"`
	Cover    *string `json:"cover,omitempty"`
	Thumb    *string `json:"thumb,omitempty"`
	Colour   *string `json:"colour,omitempty"`
	Format   *string `json:"format,omitempty"`
	OnList   bool    `json:"onList"`
	Progress int     `json:"progress"`
	Behind   int     `json:"behind"`
	Watched  bool    `json:"watched"`

	// A late-night broadcast falls on a different date in Japan than it does
	// for the viewer, and fansub sites quote the Japanese one.
	JSTWeekday string `json:"jstWeekday"`
}

type scheduleDay struct {
	Date    string         `json:"date"`
	Weekday string         `json:"weekday"`
	Today   bool           `json:"today"`
	Items   []scheduleItem `json:"items"`
}

var jst = time.FixedZone("JST", 9*3600)

func zoneFor(offsetMinutes int) *time.Location {
	return time.FixedZone("local", offsetMinutes*60)
}

func midnight(t time.Time, offsetMinutes int) time.Time {
	local := t.In(zoneFor(offsetMinutes))
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, local.Location())
}

// groupByDay splits entries into the viewer's local days; the client supplies
// the offset since the server cannot know it. Exactly count days are emitted,
// empties included, so a week yields seven tabs rather than two Mondays.
func groupByDay(items []scheduleItem, offsetMinutes int, start int64, count int) []scheduleDay {
	zone := zoneFor(offsetMinutes)
	today := time.Now().In(zone).Format("2006-01-02")

	days := make([]scheduleDay, 0, count)
	index := map[string]int{}

	cursor := time.Unix(start, 0).In(zone)
	for range count {
		date := cursor.Format("2006-01-02")
		index[date] = len(days)
		days = append(days, scheduleDay{
			Date:    date,
			Weekday: cursor.Format("Monday"),
			Today:   date == today,
			Items:   []scheduleItem{},
		})
		cursor = cursor.AddDate(0, 0, 1)
	}

	for _, item := range items {
		date := time.Unix(int64(item.AiringAt), 0).In(zone).Format("2006-01-02")
		if i, ok := index[date]; ok {
			days[i].Items = append(days[i].Items, item)
		}
	}
	return days
}

// schedule returns what airs in a window. Timestamps stay as UTC integers:
// grouping by day belongs to the client, whose timezone this is not.
func (s *Server) schedule(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	days, _ := strconv.Atoi(q.Get("days"))
	if days <= 0 || days > 28 {
		days = 7
	}

	// tz is the offset the browser reports, negated: getTimezoneOffset returns
	// minutes to add to local time to reach UTC.
	offset, grouped := 0, false
	if raw := q.Get("tz"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v >= -1440 && v <= 1440 {
			offset, grouped = v, true
		}
	}

	// Snapping to local midnight keeps the day buckets aligned with the window,
	// so asking for a week returns seven tabs rather than eight.
	start := time.Now().Add(-24 * time.Hour).Unix()
	if v, err := strconv.ParseInt(q.Get("start"), 10, 64); err == nil && v > 0 {
		start = v
	} else if grouped {
		start = time.Now().Unix()
	}
	if grouped {
		start = midnight(time.Unix(start, 0), offset).Unix()
	}
	end := start + int64(days)*24*3600

	entries, err := s.anilist.Schedule(r.Context(), start, end)
	if err != nil {
		s.fail(w, "schedule", err)
		return
	}

	onList, err := s.store.ListProgress(r.Context())
	if err != nil {
		s.log.Warn("list progress", "err", err)
	}

	mineOnly := q.Get("mine") == "true"
	titles := s.store.TitleMode(r.Context(), 0)
	items := make([]scheduleItem, 0, len(entries))

	for _, e := range entries {
		progress, listed := onList[e.MediaID]
		if mineOnly && !listed {
			continue
		}

		item := scheduleItem{
			AnimeID:  e.MediaID,
			Episode:  e.Episode,
			AiringAt: e.AiringAt,
			English:  e.Media.Title.English,
			Cover:    e.Media.CoverImage.Large,
			Thumb:    e.Media.CoverImage.Medium,
			Colour:   e.Media.CoverImage.Color,
			Format:   e.Media.Format,
			OnList:   listed,
			Progress: progress,
		}
		if e.Media.Title.Romaji != nil {
			item.Romaji = *e.Media.Title.Romaji
		}
		item.Title = store.PickTitle(titles, item.Romaji, item.English)
		item.JSTWeekday = time.Unix(int64(e.AiringAt), 0).In(jst).Format("Monday")

		// Everything before the next airing has already gone out, so anything
		// past the user's progress is waiting to be watched.
		if listed && e.Episode > 1 {
			if behind := (e.Episode - 1) - progress; behind > 0 {
				item.Behind = behind
			}
		}
		item.Watched = listed && progress >= e.Episode

		items = append(items, item)
	}

	body := map[string]any{
		"items": items,
		"count": len(items),
		"start": start,
		"end":   end,
	}

	if grouped {
		body["days"] = groupByDay(items, offset, start, days)
	}
	send(w, http.StatusOK, body)
}
