// Package library turns AniList responses into domain rows and hands them to
// the store. It holds no SQL and no HTTP concerns.
package library

import (
	"context"
	"encoding/json"
	"log/slog"

	"kuro/internal/anilist"
	"kuro/internal/store"
)

type Importer struct {
	store *store.Store
	al    *anilist.Client
	log   *slog.Logger
}

func NewImporter(s *store.Store, al *anilist.Client, log *slog.Logger) *Importer {
	return &Importer{store: s, al: al, log: log}
}

// Run mirrors the whole list locally. Remote state wins here; the mode decides
// whether local-only entries survive.
func (i *Importer) Run(ctx context.Context, userID int, mode store.ImportMode) (store.ImportResult, error) {
	entries, err := i.al.List(ctx, userID)
	if err != nil {
		return store.ImportResult{}, err
	}

	anime := make([]store.Anime, 0, len(entries))
	rows := make([]store.Entry, 0, len(entries))
	for _, e := range entries {
		anime = append(anime, toAnime(e.Media))
		rows = append(rows, toEntry(e))
	}

	res, err := i.store.ImportList(ctx, anime, rows, mode)
	if err != nil {
		return store.ImportResult{}, err
	}
	i.log.Info("list imported", "mode", mode, "entries", res.Entries, "removed", res.Removed)
	return res, nil
}

// Hydrate fills in anime the graph references but nobody has imported, which
// otherwise appear as rows with an id and no title.
func (i *Importer) Hydrate(ctx context.Context, ids []int) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	media, err := i.al.MediaByIDs(ctx, ids)
	if err != nil {
		return 0, err
	}
	return i.Save(ctx, media)
}

// Save stores media the caller already has, so viewing a page keeps the local
// record current instead of refetching later.
func (i *Importer) Save(ctx context.Context, media []anilist.Media) (int, error) {
	if len(media) == 0 {
		return 0, nil
	}
	rows := make([]store.Anime, 0, len(media))
	for _, m := range media {
		rows = append(rows, toAnime(m))
	}
	if _, err := i.store.ImportList(ctx, rows, nil, store.ImportMerge); err != nil {
		return 0, err
	}
	return len(rows), nil
}

func toAnime(m anilist.Media) store.Anime {
	a := store.Anime{
		ID: m.ID, MalID: m.IDMal,
		English: m.Title.English, Native: m.Title.Native,
		Synonyms: jsonArray(m.Synonyms), Genres: jsonArray(m.Genres),
		Studios: jsonArray(m.Studios.Names()), Tags: jsonOf(m.Tags), Links: jsonOf(m.Links),
		Format: m.Format, Status: m.Status, Episodes: m.Episodes, Duration: m.Duration,
		Season: m.Season, SeasonYear: m.SeasonYear, Country: m.CountryOfOrigin,
		IsAdult: m.IsAdult, Description: m.Description, Source: m.Source,
		Cover: m.CoverImage.ExtraLarge, CoverColor: m.CoverImage.Color,
		CoverMedium: m.CoverImage.Medium, Banner: m.BannerImage,
		AverageScore: m.AverageScore, MeanScore: m.MeanScore,
		Popularity: m.Popularity, Favourites: m.Favourites,
		StartDate: m.StartDate.String(), EndDate: m.EndDate.String(),
	}
	if m.Trailer != nil {
		a.TrailerID, a.TrailerSite = m.Trailer.ID, m.Trailer.Site
	}

	switch {
	case m.Title.Romaji != nil:
		a.Romaji = *m.Title.Romaji
	case m.Title.UserPreferred != nil:
		a.Romaji = *m.Title.UserPreferred
	}
	if m.NextAiring != nil {
		a.NextEpisode = &m.NextAiring.Episode
		a.NextAiringAt = &m.NextAiring.AiringAt
	}
	return a
}

func toEntry(e anilist.ListEntry) store.Entry {
	return store.Entry{
		ID: e.ID, AnimeID: e.MediaID, Status: e.Status,
		Progress: e.Progress, Score: e.Score, Repeat: e.Repeat, Notes: e.Notes,
		Private: e.Private, Hidden: e.Hidden,
		StartedAt: e.StartedAt.String(), CompletedAt: e.CompletedAt.String(),
		UpdatedAt: e.UpdatedAt,
	}
}

func jsonArray(v []string) string {
	if v == nil {
		v = []string{}
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func jsonOf(v any) string {
	b, err := json.Marshal(v)
	if err != nil || string(b) == "null" {
		return "[]"
	}
	return string(b)
}
