package library

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"kuro/internal/anilist"
	"kuro/internal/store"
)

// A record AniList returned this recently is not written again: a page served
// from the client's ten-minute cache holds nothing newer.
const rememberEvery = 10 * time.Minute

type recentIDs struct {
	mu   sync.Mutex
	seen map[int]time.Time
}

// fresh returns the ids not written within rememberEvery, and marks them written.
func (r *recentIDs) fresh(media []anilist.Media, now time.Time) []anilist.Media {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.seen == nil || len(r.seen) > 5000 {
		r.seen = map[int]time.Time{}
	}
	var out []anilist.Media
	for _, m := range media {
		if m.ID <= 0 || now.Sub(r.seen[m.ID]) < rememberEvery {
			continue
		}
		r.seen[m.ID] = now
		out = append(out, m)
	}
	return out
}

// Remember keeps every show AniList returns anywhere — browse, search, the
// schedule, a studio — so the local search, the anime page's fallback and the
// airing refresh know it. A show the corpus lacks is added too, so a new one
// is matchable the moment it is seen, not the day the id list catches up.
// Nothing served from the saved copy is written: it is old by definition.
func (i *Importer) Remember(ctx context.Context, media []anilist.Media) {
	if anilist.UsedSaved(ctx) {
		return
	}
	media = i.recent.fresh(media, time.Now())
	if len(media) == 0 {
		return
	}
	go i.remember(context.WithoutCancel(ctx), media)
}

func (i *Importer) remember(ctx context.Context, media []anilist.Media) {
	if _, err := i.Save(ctx, media); err != nil {
		i.log.Warn("remember anime", "count", len(media), "err", err)
		return
	}

	ids := make([]int, len(media))
	entries := make([]store.CorpusEntry, len(media))
	for n, m := range media {
		ids[n] = m.ID
		entries[n] = mediaToEntry(m)
	}
	added, err := i.store.NotInCorpus(ctx, ids)
	if err == nil {
		_, err = i.store.SaveCorpus(ctx, entries)
	}
	if err == nil {
		err = i.store.Revive(ctx, ids)
	}
	if err != nil {
		i.log.Warn("remember corpus", "count", len(media), "err", err)
		return
	}
	if len(added) > 0 && i.OnNewTitles != nil {
		i.log.Info("new anime seen", "count", len(added))
		i.OnNewTitles()
	}
}

// Refresh re-reads saved shows still airing whose row is past due, so a show
// nobody has opened since still knows its next episode and when it ended.
func (i *Importer) Refresh(ctx context.Context, limit int) (int, error) {
	ids, err := i.store.StaleAiring(ctx, time.Now(), limit)
	if err != nil || len(ids) == 0 {
		return 0, err
	}
	return i.Hydrate(ctx, ids)
}

// FromRecord turns a saved row back into the shape AniList serves, for the
// anime page when AniList cannot be reached. Studio ids are not kept, only names.
func FromRecord(a store.Anime) anilist.Media {
	m := anilist.Media{
		ID: a.ID, IDMal: a.MalID,
		Title:  anilist.Title{English: a.English, Native: a.Native},
		Format: a.Format, Status: a.Status, Episodes: a.Episodes, Duration: a.Duration,
		Season: a.Season, SeasonYear: a.SeasonYear, Description: a.Description, Source: a.Source,
		AverageScore: a.AverageScore, MeanScore: a.MeanScore,
		Popularity: a.Popularity, Favourites: a.Favourites,
		CountryOfOrigin: a.Country, IsAdult: a.IsAdult,
		CoverImage:  anilist.Cover{ExtraLarge: a.Cover, Large: a.Cover, Medium: a.CoverMedium, Color: a.CoverColor},
		BannerImage: a.Banner,
		StartDate:   parseDate(a.StartDate), EndDate: parseDate(a.EndDate),
	}
	if a.Romaji != "" {
		romaji := a.Romaji
		m.Title.Romaji = &romaji
	}
	_ = json.Unmarshal([]byte(a.Genres), &m.Genres)
	_ = json.Unmarshal([]byte(a.Synonyms), &m.Synonyms)
	_ = json.Unmarshal([]byte(a.Tags), &m.Tags)
	_ = json.Unmarshal([]byte(a.Links), &m.Links)

	var studios []string
	_ = json.Unmarshal([]byte(a.Studios), &studios)
	for _, name := range studios {
		m.Studios.Nodes = append(m.Studios.Nodes, struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		}{Name: name})
	}
	if a.TrailerID != nil {
		m.Trailer = &anilist.Trailer{ID: a.TrailerID, Site: a.TrailerSite}
	}
	// A saved broadcast time already past says nothing about the next one.
	if a.NextEpisode != nil && a.NextAiringAt != nil && *a.NextAiringAt > int(time.Now().Unix()) {
		until := *a.NextAiringAt - int(time.Now().Unix())
		m.NextAiring = &anilist.Airing{
			Episode: *a.NextEpisode, AiringAt: *a.NextAiringAt, TimeUntilAiring: until,
		}
	}
	return m
}

// parseDate reads FuzzyDate.String's YYYY-MM-DD back, zero parts unknown.
func parseDate(s string) anilist.FuzzyDate {
	var y, mo, d int
	if n, _ := fmt.Sscanf(s, "%d-%d-%d", &y, &mo, &d); n == 0 || y == 0 {
		return anilist.FuzzyDate{}
	}
	out := anilist.FuzzyDate{Year: &y}
	if mo > 0 {
		out.Month = &mo
	}
	if d > 0 {
		out.Day = &d
	}
	return out
}
