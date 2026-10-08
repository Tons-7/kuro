package library

import (
	"context"
	"regexp"
	"slices"
	"strings"

	"kuro/internal/film"
	"kuro/internal/score"
	"kuro/internal/store"
	"kuro/internal/torrent"
)

// filmSeries is one film's place in its series: the key telling it apart, and the names the series goes by.
type filmSeries struct {
	key   film.Key
	names []string
}

// queries are the series names to search; none outside a film series.
func (s *filmSeries) queries() []string {
	if s == nil {
		return nil
	}
	return s.names
}

// single reports an entry catalogued as one film: a feature, or a one-part OVA or special.
func single(format *string, episodes *int) bool {
	if format == nil || (episodes != nil && *episodes > 1) {
		return false
	}
	switch strings.ToUpper(*format) {
	case "MOVIE", "OVA", "SPECIAL", "ONA":
		return true
	}
	return false
}

// filmSeries reports the series this entry is one film of; nil otherwise, as for a film attached to a TV show.
func (f *Finder) filmSeries(ctx context.Context, animeID int) *filmSeries {
	members := f.seriesMembers(ctx, animeID)

	var own *store.Season
	for i := range members {
		if members[i].ID == animeID {
			own = &members[i]
		}
	}
	if own == nil || !single(own.Format, own.Episodes) {
		return nil
	}

	titlesOf := func(s store.Season) []string {
		titles, _ := f.store.SearchTitles(ctx, s.ID)
		titles = append(titles, s.Romaji)
		if s.English != nil {
			titles = append(titles, *s.English)
		}
		return titles
	}

	// A show under the same base name means packs of episodes, which a chapter number must not be read against.
	base := seriesBase(own.Romaji)
	if base == "" {
		return nil
	}
	var siblings []film.Entry
	var romaji, english []string
	sharing := 0
	for _, m := range members {
		if strings.EqualFold(seriesBase(m.Romaji), base) {
			if !single(m.Format, m.Episodes) {
				return nil
			}
			sharing++
			romaji = append(romaji, m.Romaji)
			if m.English != nil {
				english = append(english, *m.English)
			}
		}
		if m.ID != animeID {
			siblings = append(siblings, film.Entry{Titles: titlesOf(m)})
		}
	}
	if sharing < 2 {
		return nil
	}

	out := &filmSeries{key: film.New(film.Entry{Titles: titlesOf(*own)}, siblings)}
	for _, name := range []string{commonPrefix(romaji), commonPrefix(english)} {
		if name = cleanQuery(name); len(strings.Fields(name)) >= 2 && mostlyLatin(name) {
			out.names = append(out.names, name)
		}
	}
	return out
}

// seriesMembers is every entry of the franchise; a side story is off the chain, so it borrows its parent's.
func (f *Finder) seriesMembers(ctx context.Context, animeID int) []store.Season {
	collect := func(id int) (chain int, out []store.Season) {
		franchise, err := f.store.Franchise(ctx, id)
		if err != nil {
			return 0, nil
		}
		out = append(out, franchise.Seasons...)
		if related, err := f.store.Related(ctx, id); err == nil {
			for _, r := range related {
				out = append(out, r.Season)
			}
		}
		return len(franchise.Seasons), out
	}

	chain, members := collect(animeID)
	if chain <= 1 {
		for _, m := range members {
			if m.ID == animeID {
				continue
			}
			if longer, theirs := collect(m.ID); longer > 1 {
				members = theirs
				break
			}
		}
	}

	seen := map[int]bool{}
	return slices.DeleteFunc(members, func(m store.Season) bool {
		dup := seen[m.ID]
		seen[m.ID] = true
		return dup
	})
}

// seriesBase is a film's title before its subtitle: "Kara no Kyoukai" of "Kara no Kyoukai: Fukan Fuukei".
func seriesBase(title string) string {
	return strings.ToLower(strings.TrimSpace(baseOf(title)))
}

var trailingMarker = regexp.MustCompile(`(?i)[\s:,-]+(?:chapter|movie|film|part|vol(?:ume)?|the)?\s*\d*\s*$`)

// commonPrefix is the words every title starts with, without a dangling "Chapter".
func commonPrefix(titles []string) string {
	if len(titles) < 2 {
		return ""
	}
	prefix := strings.Fields(titles[0])
	for _, t := range titles[1:] {
		words := strings.Fields(t)
		n := 0
		for n < len(prefix) && n < len(words) && strings.EqualFold(prefix[n], words[n]) {
			n++
		}
		prefix = prefix[:n]
	}
	out := strings.Join(prefix, " ")
	for {
		trimmed := trailingMarker.ReplaceAllString(out, "")
		if trimmed == out {
			return strings.TrimSpace(out)
		}
		out = trimmed
	}
}

// A name saying it holds every film: there the one asked for is as good as stated.
var wholeSeries = regexp.MustCompile(
	`(?i)\ball[\s._-]*movies?\b|\bfull[\s._-]*series\b|\bcomplete\b|\bcollection\b|\btrilogy\b|\bbox\b|\bmovies?\s*\d+\s*[-~]\s*\d+|\[\d{1,2}\s*[-~]\s*\d{1,2}\]|全集`)

// pickFile finds what a release holds for the request: an episode by its number, a film by its name.
func pickFile(files []torrent.File, r score.Result) (torrent.File, int, bool) {
	if r.Film == nil {
		return torrent.PickEpisode(files, r.Numbers...)
	}
	named := make([]film.File, len(files))
	for i, f := range files {
		named[i] = film.File{Name: f.Name, Length: f.Length}
	}
	if i, ok := r.Film.Pick(named); ok {
		return files[i], i, true
	}
	videos := 0
	for _, f := range named {
		if film.IsVideo(f.Name) {
			videos++
		}
	}
	// A release naming this film may ship unnamed extras beside it; the film is the big one.
	// A lone file gets no such benefit: Pick refused it for naming another film.
	if !r.Release.Batch && videos > 1 {
		return torrent.PickVideo(files)
	}
	return torrent.File{}, -1, false
}
