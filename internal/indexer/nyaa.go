package indexer

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/time/rate"
)

// Prowlarr's own Nyaa definition sets a two second delay; there is no
// documented rate limit, but latency is high and variable.
const nyaaDelay = 2 * time.Second

type Nyaa struct {
	http     *http.Client
	base     string
	name     string
	category string
	limiter  *rate.Limiter
}

type NyaaOption func(*Nyaa)

func WithNyaaDelay(d time.Duration) NyaaOption {
	return func(n *Nyaa) { n.limiter = rate.NewLimiter(rate.Every(d), 1) }
}

// NewNyaa reads a Nyaa-style site at base. No site is built in.
func NewNyaa(base string, opts ...NyaaOption) *Nyaa {
	n := &Nyaa{
		http:    &http.Client{Timeout: 30 * time.Second},
		base:    strings.TrimRight(base, "/"),
		limiter: rate.NewLimiter(rate.Every(nyaaDelay), 1),
	}
	for _, opt := range opts {
		opt(n)
	}
	return n
}

func (n *Nyaa) Name() string {
	if n.name != "" {
		return n.name
	}
	return "nyaa"
}

func (n *Nyaa) defaultCategory() string {
	if n.category != "" {
		return n.category
	}
	return CategoryEnglishSubbed
}

// CategorySukebeiAnime is "Art - Anime" on Nyaa's adult sister site, whose
// numbering differs from the main site's.
const CategorySukebeiAnime = "1_1"

// NewSukebei reads an adult Nyaa-style site. The main site carries no adult
// titles at all, so a show AniList marks isAdult is findable nowhere else.
func NewSukebei(base string, opts ...NyaaOption) *Nyaa {
	adult := func(n *Nyaa) { n.name = "sukebei"; n.category = CategorySukebeiAnime }
	return NewNyaa(base, append([]NyaaOption{adult}, opts...)...)
}

type rssFeed struct {
	Items []rssItem `xml:"channel>item"`
}

// Local names only: sukebei serves the same feed under its own namespace, and
// a namespace-qualified tag silently yields empty fields against it.
type rssItem struct {
	Title     string `xml:"title"`
	Link      string `xml:"link"`
	GUID      string `xml:"guid"`
	PubDate   string `xml:"pubDate"`
	InfoHash  string `xml:"infoHash"`
	Seeders   string `xml:"seeders"`
	Leechers  string `xml:"leechers"`
	Downloads string `xml:"downloads"`
	Category  string `xml:"category"`
	Size      string `xml:"size"`
	Trusted   string `xml:"trusted"`
	Remake    string `xml:"remake"`
}

// Search ranks by seeders via the HTML listing. Latest reads the RSS feed, which
// is chronological and capped at 75 — for polling new releases, not old episodes.
func (n *Nyaa) Search(ctx context.Context, q Query) ([]Torrent, error) {
	if q.Text == "" {
		return n.Latest(ctx, q)
	}
	return n.searchHTML(ctx, q)
}

func (n *Nyaa) Latest(ctx context.Context, q Query) ([]Torrent, error) {
	if err := n.limiter.Wait(ctx); err != nil {
		return nil, err
	}

	params := url.Values{
		"page": {"rss"},
		"c":    {orDefault(q.Category, n.defaultCategory())},
		"f":    {strconv.Itoa(int(q.Filter))},
	}
	if q.Text != "" {
		params.Set("q", q.Text)
	}
	if q.Uploader != "" {
		params.Set("u", q.Uploader)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, n.base+"/?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "kuro/0.1")

	res, err := n.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("nyaa: HTTP %d", res.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return nil, err
	}

	var feed rssFeed
	if err := xml.Unmarshal(body, &feed); err != nil {
		return nil, fmt.Errorf("nyaa: parse feed: %w", err)
	}

	out := make([]Torrent, 0, len(feed.Items))
	for _, item := range feed.Items {
		if item.InfoHash == "" {
			continue
		}
		out = append(out, Torrent{
			Title:        strings.TrimSpace(item.Title),
			InfoHash:     strings.ToLower(strings.TrimSpace(item.InfoHash)),
			Size:         ParseSize(item.Size),
			Seeders:      atoi(item.Seeders),
			Leechers:     atoi(item.Leechers),
			SeedersKnown: true,
			Downloads:    atoi(item.Downloads),
			Category:     item.Category,
			Trusted:      strings.EqualFold(item.Trusted, "yes"),
			Remake:       strings.EqualFold(item.Remake, "yes"),
			Published:    parseTime(item.PubDate),
			Link:         item.GUID,
			Indexer:      "nyaa",
		})
		if q.Limit > 0 && len(out) >= q.Limit {
			break
		}
	}
	return out, nil
}

var sizeUnits = map[string]int64{
	"b": 1, "kib": 1 << 10, "mib": 1 << 20, "gib": 1 << 30, "tib": 1 << 40,
	"kb": 1000, "mb": 1000 * 1000, "gb": 1000 * 1000 * 1000, "tb": 1000 * 1000 * 1000 * 1000,
}

// Nyaa writes "8.2 GiB", TokyoTosho "1.35GB".
var sizeValue = regexp.MustCompile(`^([0-9][0-9,.]*)\s*([a-zA-Z]+)$`)

// ParseSize converts a human-readable size into bytes.
func ParseSize(s string) int64 {
	m := sizeValue.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0
	}
	value, err := strconv.ParseFloat(strings.ReplaceAll(m[1], ",", ""), 64)
	if err != nil {
		return 0
	}
	unit, ok := sizeUnits[strings.ToLower(m[2])]
	if !ok {
		return 0
	}
	return int64(value * float64(unit))
}

func parseTime(s string) time.Time {
	for _, layout := range []string{time.RFC1123Z, time.RFC1123, time.RFC822Z, time.RFC822} {
		if t, err := time.Parse(layout, strings.TrimSpace(s)); err == nil {
			return t
		}
	}
	return time.Time{}
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
