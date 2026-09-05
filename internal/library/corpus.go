package library

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"kuro/internal/anilist"
	"kuro/internal/corpus"
	"kuro/internal/store"
)

const (
	// AniList accepts 50 ids per request.
	batchSize = 50

	seedInterval     = 30 * 24 * time.Hour
	discoverInterval = 24 * time.Hour
)

type Ingester struct {
	store   *store.Store
	fetcher *corpus.Fetcher
	al      *anilist.Client
	log     *slog.Logger
}

func NewIngester(s *store.Store, f *corpus.Fetcher, al *anilist.Client, log *slog.Logger) *Ingester {
	return &Ingester{store: s, fetcher: f, al: al, log: log}
}

type Report struct {
	Seeded     int `json:"seeded"`
	Discovered int `json:"discovered"`
	Fetched    int `json:"fetched"`
	Titles     int `json:"titles"`
	Dead       int `json:"dead"`
}

func (i *Ingester) Run(ctx context.Context, force bool) (Report, error) {
	var rep Report

	if due, _ := i.due(ctx, "manami", seedInterval, force); due {
		n, err := i.seed(ctx)
		if err != nil {
			return rep, fmt.Errorf("seed: %w", err)
		}
		rep.Seeded = n
	}

	if due, _ := i.due(ctx, "animeapi", discoverInterval, force); due {
		missing, err := i.discover(ctx)
		if err != nil {
			return rep, fmt.Errorf("discover: %w", err)
		}
		rep.Discovered = len(missing)

		rep.Fetched, rep.Titles, rep.Dead, err = i.fetchTitles(ctx, missing)
		if err != nil {
			return rep, fmt.Errorf("fetch titles: %w", err)
		}
	}
	return rep, nil
}

func (i *Ingester) due(ctx context.Context, name string, every time.Duration, force bool) (bool, error) {
	if force {
		return true, nil
	}
	at, err := i.store.SourceRefreshedAt(ctx, name)
	if err != nil {
		return true, err
	}
	return at.IsZero() || time.Since(at) > every, nil
}

// seed loads the frozen manami database; it never changes, so this runs once.
func (i *Ingester) seed(ctx context.Context) (int, error) {
	i.log.Info("seeding title corpus")

	batch := make([]store.CorpusEntry, 0, 2000)
	var total, titles int

	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		n, err := i.store.SaveCorpus(ctx, batch)
		if err != nil {
			return err
		}
		titles += n
		batch = batch[:0]
		return nil
	}

	count, err := i.fetcher.Manami(ctx, func(e corpus.Entry) error {
		batch = append(batch, toStoreEntry(e))
		total++
		if len(batch) >= 2000 {
			return flush()
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	if err := flush(); err != nil {
		return 0, err
	}

	i.log.Info("corpus seeded", "anime", total, "titles", titles)
	return total, i.store.MarkSource(ctx, "manami", count)
}

// discover diffs a daily-rebuilt id list against stored ids to notice new seasons.
func (i *Ingester) discover(ctx context.Context) ([]int, error) {
	live, err := i.fetcher.AnimeAPI(ctx)
	if err != nil {
		return nil, err
	}
	known, err := i.store.KnownIDs(ctx)
	if err != nil {
		return nil, err
	}

	missing := make([]int, 0, 512)
	for id := range live {
		if _, seen := known[id]; !seen {
			missing = append(missing, id)
		}
	}

	i.log.Info("id discovery", "upstream", len(live), "known", len(known), "new", len(missing))
	return missing, i.store.MarkSource(ctx, "animeapi", len(live))
}

// Ids that return nothing are recorded so they are never requested again.
func (i *Ingester) fetchTitles(ctx context.Context, ids []int) (fetched, titles, dead int, err error) {
	for start := 0; start < len(ids); start += batchSize {
		chunk := ids[start:min(start+batchSize, len(ids))]

		media, err := i.al.MediaByIDs(ctx, chunk)
		if err != nil {
			return fetched, titles, dead, err
		}

		entries := make([]store.CorpusEntry, 0, len(media))
		found := make(map[int]struct{}, len(media))
		for _, m := range media {
			found[m.ID] = struct{}{}
			entries = append(entries, mediaToEntry(m))
		}

		n, err := i.store.SaveCorpus(ctx, entries)
		if err != nil {
			return fetched, titles, dead, err
		}
		fetched += len(entries)
		titles += n

		var gone []int
		for _, id := range chunk {
			if _, ok := found[id]; !ok {
				gone = append(gone, id)
			}
		}
		if err := i.store.MarkDead(ctx, gone); err != nil {
			return fetched, titles, dead, err
		}
		dead += len(gone)
	}
	return fetched, titles, dead, nil
}

func toStoreEntry(e corpus.Entry) store.CorpusEntry {
	out := store.CorpusEntry{
		AniListID: e.AniListID, MalID: e.MalID, AniDBID: e.AniDBID,
		Kind: e.Kind, Episodes: e.Episodes, Year: e.Year, Season: e.Season,
	}
	for _, t := range e.Titles {
		out.Titles = append(out.Titles, store.CorpusTitle{Text: t.Text, Kind: t.Kind, Lang: t.Lang})
	}
	return out
}

func mediaToEntry(m anilist.Media) store.CorpusEntry {
	out := store.CorpusEntry{AniListID: m.ID}
	if m.IDMal != nil {
		out.MalID = *m.IDMal
	}
	if m.Format != nil {
		out.Kind = *m.Format
	}
	if m.Episodes != nil {
		out.Episodes = *m.Episodes
	}
	if m.SeasonYear != nil {
		out.Year = *m.SeasonYear
	}
	if m.Season != nil {
		out.Season = *m.Season
	}

	add := func(text, kind string) {
		if text != "" {
			out.Titles = append(out.Titles, store.CorpusTitle{Text: text, Kind: kind})
		}
	}
	if m.Title.Romaji != nil {
		add(*m.Title.Romaji, "primary")
	}
	if m.Title.English != nil {
		add(*m.Title.English, "official")
	}
	if m.Title.Native != nil {
		add(*m.Title.Native, "native")
	}
	for _, s := range m.Synonyms {
		add(s, "synonym")
	}
	return out
}
