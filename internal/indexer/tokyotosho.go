package indexer

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"golang.org/x/time/rate"
)

const (
	// Every uncached search waits on this source too, so it is paced like the
	// aggregator it replaced rather than like Nyaa.
	tokyoDelay = 1200 * time.Millisecond

	// rss.php's category filter. 1 is Anime.
	tokyoAnimeCategory = "1"
)

// TokyoTosho indexes Nyaa, AniDex and its own submissions. Its feed carries no
// peer counts, so results come back with seeders unknown rather than zero.
type TokyoTosho struct {
	http     *http.Client
	base     string
	category string
	limiter  *rate.Limiter
}

type TokyoToshoOption func(*TokyoTosho)

func WithTokyoToshoDelay(d time.Duration) TokyoToshoOption {
	return func(t *TokyoTosho) { t.limiter = rate.NewLimiter(rate.Every(d), 1) }
}

// NewTokyoTosho reads a TokyoTosho-style site at base. No site is built in.
func NewTokyoTosho(base string, opts ...TokyoToshoOption) *TokyoTosho {
	t := &TokyoTosho{
		http:     &http.Client{Timeout: 30 * time.Second},
		base:     strings.TrimRight(base, "/"),
		category: tokyoAnimeCategory,
		limiter:  rate.NewLimiter(rate.Every(tokyoDelay), 1),
	}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

func (t *TokyoTosho) Name() string { return "tokyotosho" }

type tokyoFeed struct {
	Items []tokyoItem `xml:"channel>item"`
}

type tokyoItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	GUID        string `xml:"guid"`
	PubDate     string `xml:"pubDate"`
	Category    string `xml:"category"`
	Description string `xml:"description"`
}

var (
	tokyoMagnet     = regexp.MustCompile(`href="(magnet:[^"]+)"`)
	tokyoSize       = regexp.MustCompile(`(?i)Size:\s*([0-9.,]+\s*[a-z]+)`)
	tokyoAuthorized = regexp.MustCompile(`(?i)Authorized:\s*(\w+)`)
)

func (t *TokyoTosho) Search(ctx context.Context, q Query) ([]Torrent, error) {
	if err := t.limiter.Wait(ctx); err != nil {
		return nil, err
	}

	params := url.Values{"filter": {t.category}}
	if q.Text != "" {
		params.Set("terms", q.Text)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		t.base+"/rss.php?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	// Without one the site serves an empty body.
	req.Header.Set("User-Agent", "kuro/0.1")
	req.Header.Set("Accept", "application/rss+xml")

	res, err := t.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tokyotosho: HTTP %d", res.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return nil, err
	}

	// A query with no matches answers with the closing tags alone, which is not
	// a document. No items is no results, not a failure.
	if !bytes.Contains(body, []byte("<item>")) {
		return nil, nil
	}

	var feed tokyoFeed
	if err := xml.Unmarshal(body, &feed); err != nil {
		return nil, fmt.Errorf("tokyotosho: parse feed: %w", err)
	}

	out := make([]Torrent, 0, len(feed.Items))
	for _, item := range feed.Items {
		torrent, ok := t.torrent(item)
		if !ok {
			continue
		}
		out = append(out, torrent)
		if q.Limit > 0 && len(out) >= q.Limit {
			break
		}
	}
	return out, nil
}

func (t *TokyoTosho) torrent(item tokyoItem) (Torrent, bool) {
	title := strings.TrimSpace(item.Title)
	if title == "" {
		return Torrent{}, false
	}

	magnet := tokyoMagnet.FindStringSubmatch(item.Description)
	if magnet == nil {
		return Torrent{}, false
	}
	hash := infoHashFromMagnet(html.UnescapeString(magnet[1]))
	if hash == "" {
		return Torrent{}, false
	}

	out := Torrent{
		Title:     title,
		InfoHash:  hash,
		Category:  strings.TrimSpace(item.Category),
		Published: parseTime(item.PubDate),
		Link:      strings.TrimSpace(item.GUID),
		Indexer:   t.Name(),
	}
	if m := tokyoSize.FindStringSubmatch(item.Description); m != nil {
		out.Size = ParseSize(m[1])
	}
	if m := tokyoAuthorized.FindStringSubmatch(item.Description); m != nil {
		out.Trusted = strings.EqualFold(m[1], "yes")
	}
	return out, true
}
