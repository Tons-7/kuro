// Package corpus builds the local index of every anime and every name it goes
// by, so a release can be matched offline.
//
// Three sources, each doing the one job it is best at:
//
//	manami     one-time seed, ~20,700 anime with ~9 synonyms each. Frozen since
//	           July 2026, so it never learns about a new season.
//	animeApi   daily rebuild, ids only. This is how new anime are discovered.
//	AniList    authoritative titles, fetched 50 ids at a time for whatever the
//	           other two do not already cover.
package corpus

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
)

const (
	ManamiURL   = "https://github.com/manami-project/anime-offline-database/releases/download/latest/anime-offline-database.jsonl.zst"
	AnimeAPIURL = "https://raw.githubusercontent.com/nattadasu/animeApi/v3/database/animeapi.tsv"
	DeadIDsURL  = "https://github.com/manami-project/anime-offline-database/releases/download/latest/anilist-minified.json.zst"
	AniDBURL    = "https://anidb.net/api/anime-titles.dat.gz"

	// AniDB rejects requests without a distinct User-Agent and allows one
	// download per day.
	userAgent = "kuro/0.1 (personal media library)"
)

type Entry struct {
	AniListID int
	MalID     int
	AniDBID   int
	Kind      string
	Episodes  int
	Year      int
	Season    string
	Titles    []Title
}

type Title struct {
	Text string
	Kind string
	Lang string
}

type Fetcher struct {
	http *http.Client
	urls map[string]string
}

func NewFetcher() *Fetcher {
	return &Fetcher{
		http: &http.Client{Timeout: 5 * time.Minute},
		urls: map[string]string{
			"manami":   ManamiURL,
			"animeapi": AnimeAPIURL,
			"anidb":    AniDBURL,
		},
	}
}

func (f *Fetcher) SetURL(name, url string) { f.urls[name] = url }

func (f *Fetcher) get(ctx context.Context, url string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)

	res, err := f.http.Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		res.Body.Close()
		return nil, fmt.Errorf("%s: HTTP %d", url, res.StatusCode)
	}
	return res.Body, nil
}

var aniListSource = regexp.MustCompile(`anilist\.co/anime/(\d+)`)
var aniDBSource = regexp.MustCompile(`anidb\.net/anime/(\d+)`)
var malSource = regexp.MustCompile(`myanimelist\.net/anime/(\d+)`)

type manamiRecord struct {
	Sources  []string `json:"sources"`
	Title    string   `json:"title"`
	Type     string   `json:"type"`
	Episodes int      `json:"episodes"`
	Synonyms []string `json:"synonyms"`
	Season   struct {
		Season string `json:"season"`
		Year   int    `json:"year"`
	} `json:"animeSeason"`
}

// Manami streams the seed database. Records without an AniList id are skipped,
// since the app is keyed on AniList throughout.
func (f *Fetcher) Manami(ctx context.Context, yield func(Entry) error) (int, error) {
	body, err := f.get(ctx, f.urls["manami"])
	if err != nil {
		return 0, err
	}
	defer body.Close()

	dec, err := zstd.NewReader(body)
	if err != nil {
		return 0, err
	}
	defer dec.Close()

	scan := bufio.NewScanner(dec.IOReadCloser())
	scan.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	var kept int
	for scan.Scan() {
		line := scan.Bytes()
		if len(line) == 0 {
			continue
		}

		var rec manamiRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}

		malID := firstID(malSource, rec.Sources)

		// MAL-only titles (no AniList id) are keyed on the negated MAL id, which
		// cannot collide with an AniList id and marks them MAL-sourced.
		id := firstID(aniListSource, rec.Sources)
		if id == 0 {
			if malID == 0 {
				continue
			}
			id = -malID
		}

		entry := Entry{
			AniListID: id,
			MalID:     malID,
			AniDBID:   firstID(aniDBSource, rec.Sources),
			Kind:      rec.Type,
			Episodes:  rec.Episodes,
			Year:      rec.Season.Year,
			Season:    rec.Season.Season,
			Titles:    []Title{{Text: rec.Title, Kind: "primary"}},
		}
		for _, syn := range rec.Synonyms {
			entry.Titles = append(entry.Titles, Title{Text: syn, Kind: "synonym"})
		}

		if err := yield(entry); err != nil {
			return kept, err
		}
		kept++
	}
	return kept, scan.Err()
}

func firstID(re *regexp.Regexp, sources []string) int {
	for _, s := range sources {
		if m := re.FindStringSubmatch(s); m != nil {
			n, _ := strconv.Atoi(m[1])
			return n
		}
	}
	return 0
}

// AnimeAPI returns the AniList ids that exist today. It carries no titles, so
// its only job is telling us which anime we have never heard of.
func (f *Fetcher) AnimeAPI(ctx context.Context) (map[int]Entry, error) {
	body, err := f.get(ctx, f.urls["animeapi"])
	if err != nil {
		return nil, err
	}
	defer body.Close()

	scan := bufio.NewScanner(body)
	scan.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	if !scan.Scan() {
		return nil, fmt.Errorf("animeapi: empty response")
	}
	col := make(map[string]int)
	for i, name := range strings.Split(scan.Text(), "\t") {
		col[strings.TrimSpace(name)] = i
	}
	if _, ok := col["anilist"]; !ok {
		return nil, fmt.Errorf("animeapi: no anilist column in header")
	}

	out := make(map[int]Entry, 24000)
	for scan.Scan() {
		fields := strings.Split(scan.Text(), "\t")
		id := field(fields, col, "anilist")
		if id == 0 {
			continue
		}
		out[id] = Entry{
			AniListID: id,
			MalID:     field(fields, col, "myanimelist"),
			AniDBID:   field(fields, col, "anidb"),
		}
	}
	return out, scan.Err()
}

func field(fields []string, col map[string]int, name string) int {
	i, ok := col[name]
	if !ok || i >= len(fields) {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(fields[i]))
	return n
}

// AniDB publishes a daily title dump covering 70 languages. Its terms allow one
// download per day and require a distinct User-Agent.
func (f *Fetcher) AniDBTitles(ctx context.Context, yield func(anidbID int, t Title) error) (int, error) {
	body, err := f.get(ctx, f.urls["anidb"])
	if err != nil {
		return 0, err
	}
	defer body.Close()

	gz, err := gzip.NewReader(body)
	if err != nil {
		return 0, err
	}
	defer gz.Close()

	scan := bufio.NewScanner(gz)
	scan.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var n int
	for scan.Scan() {
		line := scan.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "|", 4)
		if len(parts) != 4 {
			continue
		}
		id, err := strconv.Atoi(parts[0])
		if err != nil {
			continue
		}
		if err := yield(id, Title{
			Text: parts[3],
			Kind: anidbKind(parts[1]),
			Lang: parts[2],
		}); err != nil {
			return n, err
		}
		n++
	}
	return n, scan.Err()
}

// Types 5 and 6 appear in the data but not in the file's own header.
func anidbKind(code string) string {
	switch code {
	case "1":
		return "primary"
	case "2":
		return "synonym"
	case "3":
		return "short"
	case "4":
		return "official"
	default:
		return "other"
	}
}
