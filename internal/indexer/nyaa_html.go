package indexer

import (
	"context"
	"encoding/base32"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// The RSS feed returns only the 75 newest uploads and ignores sort/page params;
// the HTML listing honours them, the only way to reach back-catalogue episodes.
func (n *Nyaa) searchHTML(ctx context.Context, q Query) ([]Torrent, error) {
	if err := n.limiter.Wait(ctx); err != nil {
		return nil, err
	}

	params := url.Values{
		"c": {orDefault(q.Category, n.defaultCategory())},
		"f": {strconv.Itoa(int(q.Filter))},
		"s": {"seeders"},
		"o": {"desc"},
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

	doc, err := html.Parse(res.Body)
	if err != nil {
		return nil, fmt.Errorf("nyaa: parse listing: %w", err)
	}

	var out []Torrent
	for _, row := range findRows(doc) {
		if t, ok := parseRow(row); ok {
			out = append(out, t)
			if q.Limit > 0 && len(out) >= q.Limit {
				break
			}
		}
	}
	return out, nil
}

func findRows(n *html.Node) []*html.Node {
	var rows []*html.Node
	var walk func(*html.Node, bool)

	walk = func(node *html.Node, inList bool) {
		if node.Type == html.ElementNode {
			switch node.Data {
			case "table":
				if strings.Contains(attr(node, "class"), "torrent-list") {
					inList = true
				}
			case "tr":
				if inList && attr(node, "class") != "" {
					rows = append(rows, node)
					return
				}
			}
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c, inList)
		}
	}

	walk(n, false)
	return rows
}

// Columns: category, title, links, size, date, seeders, leechers, completed.
// The title cell has two anchors when a torrent has comments, so select by href.
func parseRow(row *html.Node) (Torrent, bool) {
	cells := children(row, "td")
	if len(cells) < 7 {
		return Torrent{}, false
	}

	t := Torrent{Indexer: "nyaa"}
	t.Category = strings.TrimSpace(attr(findFirst(cells[0], "a"), "title"))

	for _, a := range descendants(cells[1], "a") {
		href := attr(a, "href")
		if strings.HasPrefix(href, "/view/") && !strings.Contains(href, "#comments") {
			t.Title = strings.TrimSpace(attr(a, "title"))
			if t.Title == "" {
				t.Title = text(a)
			}
			t.Link = href
		}
	}
	if t.Title == "" {
		return Torrent{}, false
	}

	for _, a := range descendants(cells[2], "a") {
		if href := attr(a, "href"); strings.HasPrefix(href, "magnet:") {
			t.InfoHash = infoHashFromMagnet(href)
		}
	}
	if t.InfoHash == "" {
		return Torrent{}, false
	}

	t.Size = ParseSize(text(cells[3]))
	if ts, err := strconv.ParseInt(attr(cells[4], "data-timestamp"), 10, 64); err == nil {
		t.Published = time.Unix(ts, 0)
	}
	t.Seeders = atoi(text(cells[5]))
	t.Leechers = atoi(text(cells[6]))
	t.SeedersKnown = true
	if len(cells) > 7 {
		t.Downloads = atoi(text(cells[7]))
	}

	// Nyaa colours rows rather than labelling them: trusted is green, remake
	// is red.
	switch class := attr(row, "class"); {
	case strings.Contains(class, "success"):
		t.Trusted = true
	case strings.Contains(class, "danger"):
		t.Remake = true
	}
	return t, true
}

func infoHashFromMagnet(magnet string) string {
	const prefix = "xt=urn:btih:"
	i := strings.Index(magnet, prefix)
	if i < 0 {
		return ""
	}
	rest := magnet[i+len(prefix):]
	if j := strings.IndexByte(rest, '&'); j >= 0 {
		rest = rest[:j]
	}
	rest = strings.TrimSpace(rest)

	// TokyoTosho writes the hash in base32; everything downstream wants hex.
	if len(rest) == 32 {
		raw, err := base32.StdEncoding.WithPadding(base32.NoPadding).
			DecodeString(strings.ToUpper(rest))
		if err != nil {
			return ""
		}
		return hex.EncodeToString(raw)
	}
	return strings.ToLower(rest)
}

func attr(n *html.Node, name string) string {
	if n == nil {
		return ""
	}
	for _, a := range n.Attr {
		if a.Key == name {
			return a.Val
		}
	}
	return ""
}

func children(n *html.Node, tag string) []*html.Node {
	var out []*html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data == tag {
			out = append(out, c)
		}
	}
	return out
}

func descendants(n *html.Node, tag string) []*html.Node {
	var out []*html.Node
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == tag {
			out = append(out, node)
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return out
}

func findFirst(n *html.Node, tag string) *html.Node {
	if got := descendants(n, tag); len(got) > 0 {
		return got[0]
	}
	return nil
}

func text(n *html.Node) string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.TextNode {
			b.WriteString(node.Data)
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.TrimSpace(b.String())
}
