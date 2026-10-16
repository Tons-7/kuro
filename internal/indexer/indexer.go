// Package indexer finds torrents. Sources sit behind one interface so Nyaa can
// be the zero-config default and Prowlarr an optional backend later.
package indexer

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
)

type Torrent struct {
	Title    string `json:"title"`
	InfoHash string `json:"infoHash"`
	Size     int64  `json:"size"`
	Seeders  int    `json:"seeders"`
	Leechers int    `json:"leechers"`
	// False when the indexer publishes no peer counts at all.
	SeedersKnown bool      `json:"seedersKnown"`
	Downloads    int       `json:"downloads"`
	Category     string    `json:"category"`
	Trusted      bool      `json:"trusted"`
	Remake       bool      `json:"remake"`
	Published    time.Time `json:"published"`
	Link         string    `json:"link"`
	Indexer      string    `json:"indexer"`
	// Trackers: the release's own, from its magnet, where most of its swarm announces.
	Trackers []string `json:"trackers,omitempty"`
}

type Query struct {
	Text     string
	Category string
	Filter   Filter
	Uploader string
	Limit    int
}

type Filter int

const (
	FilterNone     Filter = 0
	FilterNoRemake Filter = 1
	FilterTrusted  Filter = 2
)

// Nyaa categories: English-translated has subtitles; Raw is a broadcast capture with none.
const (
	CategoryAnime          = "1_0"
	CategoryEnglishSubbed  = "1_2"
	CategoryNonEnglishSubs = "1_3"
	CategoryRaw            = "1_4"
)

type Source interface {
	Name() string
	Search(ctx context.Context, q Query) ([]Torrent, error)
}

// Public trackers, after the release's own: a feed may carry only an infohash.
var trackers = []string{
	"udp://open.stealth.si:80/announce",
	"udp://tracker.opentrackr.org:1337/announce",
	"udp://exodus.desync.com:6969/announce",
	"udp://tracker.torrent.eu.org:451/announce",
}

// nyaaTracker is where Nyaa's and Sukebei's swarms announce first; the RSS feed names no tracker at all.
const nyaaTracker = "http://nyaa.tracker.wf:7777/announce"

func (t Torrent) Magnet() string {
	if t.InfoHash == "" {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "magnet:?xt=urn:btih:%s", t.InfoHash)
	if t.Title != "" {
		fmt.Fprintf(&b, "&dn=%s", url.QueryEscape(t.Title))
	}
	var first []string
	if t.Indexer == "nyaa" {
		first = []string{nyaaTracker}
	}
	seen := map[string]bool{}
	// A new slice: appending to t.Trackers could write into its spare room while another magnet is built.
	for _, tr := range slices.Concat(first, t.Trackers, trackers) {
		if tr == "" || seen[tr] {
			continue
		}
		seen[tr] = true
		fmt.Fprintf(&b, "&tr=%s", url.QueryEscape(tr))
	}
	return b.String()
}

// trackersOf reads a magnet's tr= trackers, in its order.
func trackersOf(magnet string) []string {
	_, query, ok := strings.Cut(magnet, "?")
	if !ok {
		return nil
	}
	// A bad part elsewhere (a raw % in the name) fails the parse but not the trackers it read.
	values, _ := url.ParseQuery(query)
	return values["tr"]
}

// Multi returns results from every source, deduplicated by infohash. A failing
// source is skipped, so one dead indexer doesn't hide the others' results.
type Multi struct {
	Sources []Source
}

func (m Multi) Name() string { return "multi" }

// Search asks every source at once, so one slow indexer doesn't set the wait for all.
func (m Multi) Search(ctx context.Context, q Query) ([]Torrent, error) {
	type result struct {
		torrents []Torrent
		err      error
	}
	results := make([]result, len(m.Sources))

	var wg sync.WaitGroup
	for i, src := range m.Sources {
		wg.Go(func() {
			t, err := src.Search(ctx, q)
			if err != nil {
				err = fmt.Errorf("%s: %w", src.Name(), err)
			}
			results[i] = result{torrents: t, err: err}
		})
	}
	wg.Wait()

	seen := make(map[string]int)
	var out []Torrent
	var errs []error

	// Merged in source order, so the preferred indexer's record wins a shared torrent.
	for _, r := range results {
		if r.err != nil {
			errs = append(errs, r.err)
			continue
		}
		for _, t := range r.torrents {
			key := strings.ToLower(t.InfoHash)
			if i, dup := seen[key]; dup {
				// Keep the healthier record, and any count over none at all.
				if t.SeedersKnown && (!out[i].SeedersKnown || t.Seeders > out[i].Seeders) {
					out[i].Seeders = t.Seeders
					out[i].Leechers = t.Leechers
					out[i].SeedersKnown = true
				}
				// Trusted by either site counts; a remake flag from either stands.
				out[i].Trusted = out[i].Trusted || t.Trusted
				out[i].Remake = out[i].Remake || t.Remake
				if out[i].Category == "" {
					out[i].Category = t.Category
				}
				continue
			}
			seen[key] = len(out)
			out = append(out, t)
		}
	}

	// Only when no site answered: one empty and one down is still an answer.
	if len(errs) == len(results) && len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return out, nil
}
