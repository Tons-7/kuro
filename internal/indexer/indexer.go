// Package indexer finds torrents. Sources sit behind one interface so Nyaa can
// be the zero-config default and Prowlarr an optional backend later.
package indexer

import (
	"context"
	"fmt"
	"net/url"
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

// Nyaa category codes. English-translated guarantees subtitles exist; Raw is a
// Japanese broadcast capture with none.
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

// Public trackers, added because a feed carries only an infohash and a magnet has to be rebuilt from it.
var trackers = []string{
	"udp://open.stealth.si:80/announce",
	"udp://tracker.opentrackr.org:1337/announce",
	"udp://exodus.desync.com:6969/announce",
	"udp://tracker.torrent.eu.org:451/announce",
}

func (t Torrent) Magnet() string {
	if t.InfoHash == "" {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "magnet:?xt=urn:btih:%s", t.InfoHash)
	if t.Title != "" {
		fmt.Fprintf(&b, "&dn=%s", url.QueryEscape(t.Title))
	}
	for _, tr := range trackers {
		fmt.Fprintf(&b, "&tr=%s", url.QueryEscape(tr))
	}
	return b.String()
}

// Multi returns results from every source, deduplicated by infohash. A failing
// source is skipped, so one dead indexer doesn't hide the others' results.
type Multi struct {
	Sources []Source
}

func (m Multi) Name() string { return "multi" }

// Search asks every source at once. Sequentially, one slow indexer set the
// wait for all of them, and these routinely take seconds each.
func (m Multi) Search(ctx context.Context, q Query) ([]Torrent, error) {
	type result struct {
		torrents []Torrent
		err      error
	}
	results := make([]result, len(m.Sources))

	var wg sync.WaitGroup
	for i, src := range m.Sources {
		wg.Add(1)
		go func() {
			defer wg.Done()
			t, err := src.Search(ctx, q)
			if err != nil {
				err = fmt.Errorf("%s: %w", src.Name(), err)
			}
			results[i] = result{torrents: t, err: err}
		}()
	}
	wg.Wait()

	seen := make(map[string]int)
	var out []Torrent
	var errs []error

	// Merged in source order so the preferred indexer decides the record kept
	// for a torrent both of them carry.
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
				continue
			}
			seen[key] = len(out)
			out = append(out, t)
		}
	}

	if len(out) == 0 && len(errs) > 0 {
		return nil, errs[0]
	}
	return out, nil
}
