package indexer

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

const feedXML = `<?xml version="1.0" encoding="utf-8"?>
<rss version="2.0" xmlns:nyaa="https://feed.example/xmlns/nyaa">
<channel>
  <item>
    <title>[SubsPlease] Sousou no Frieren - 10 (1080p) [7D35515E].mkv</title>
    <link>https://feed.example/download/1.torrent</link>
    <guid isPermaLink="true">https://feed.example/view/1</guid>
    <pubDate>Thu, 06 Aug 2026 17:50:54 -0000</pubDate>
    <nyaa:seeders>189</nyaa:seeders>
    <nyaa:leechers>19</nyaa:leechers>
    <nyaa:downloads>859</nyaa:downloads>
    <nyaa:infoHash>8011219944FE0000000000000000000000012E9A</nyaa:infoHash>
    <nyaa:categoryId>1_2</nyaa:categoryId>
    <nyaa:category>Anime - English-translated</nyaa:category>
    <nyaa:size>1.4 GiB</nyaa:size>
    <nyaa:trusted>Yes</nyaa:trusted>
    <nyaa:remake>No</nyaa:remake>
  </item>
  <item>
    <title>[Group] Some Show - 01 (720p).mkv</title>
    <guid isPermaLink="true">https://feed.example/view/2</guid>
    <pubDate>Thu, 06 Aug 2026 12:00:00 -0000</pubDate>
    <nyaa:seeders>3</nyaa:seeders>
    <nyaa:leechers>1</nyaa:leechers>
    <nyaa:downloads>7</nyaa:downloads>
    <nyaa:infoHash>abcdef0000000000000000000000000000000001</nyaa:infoHash>
    <nyaa:category>Anime - English-translated</nyaa:category>
    <nyaa:size>512.0 MiB</nyaa:size>
    <nyaa:trusted>No</nyaa:trusted>
    <nyaa:remake>Yes</nyaa:remake>
  </item>
  <item>
    <title>Entry with no hash</title>
    <guid isPermaLink="true">https://feed.example/view/3</guid>
    <nyaa:size>1.0 GiB</nyaa:size>
  </item>
</channel>
</rss>`

func testNyaa(t *testing.T, h http.HandlerFunc) *Nyaa {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return NewNyaa(srv.URL, WithNyaaDelay(time.Nanosecond))
}

func TestNyaaLatestParsesFeed(t *testing.T) {
	var got url.Values
	n := testNyaa(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()
		io.WriteString(w, feedXML)
	})

	results, err := n.Latest(context.Background(), Query{Text: "frieren", Filter: FilterTrusted})
	if err != nil {
		t.Fatal(err)
	}

	// The entry without an infohash cannot be downloaded, so it is dropped.
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}

	first := results[0]
	if first.Seeders != 189 || first.Leechers != 19 || first.Downloads != 859 {
		t.Errorf("peers = %+v", first)
	}
	if first.Size != 1503238553 {
		t.Errorf("size = %d, want 1.4 GiB in bytes", first.Size)
	}
	if !first.Trusted || first.Remake {
		t.Errorf("trusted=%v remake=%v", first.Trusted, first.Remake)
	}
	if first.InfoHash != strings.ToLower(first.InfoHash) {
		t.Errorf("infohash not normalised: %q", first.InfoHash)
	}
	if first.Published.IsZero() {
		t.Error("pubDate not parsed")
	}
	if first.Indexer != "nyaa" {
		t.Errorf("indexer = %q", first.Indexer)
	}

	if got.Get("q") != "frieren" {
		t.Errorf("q = %q", got.Get("q"))
	}
	if got.Get("f") != "2" {
		t.Errorf("filter = %q, want trusted-only", got.Get("f"))
	}
	// English-translated guarantees subtitles exist; Raw has none.
	if got.Get("c") != CategoryEnglishSubbed {
		t.Errorf("category = %q, want %q", got.Get("c"), CategoryEnglishSubbed)
	}
	if got.Get("page") != "rss" {
		t.Errorf("page = %q", got.Get("page"))
	}
}

func TestNyaaLatestLimit(t *testing.T) {
	n := testNyaa(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, feedXML) })

	results, err := n.Latest(context.Background(), Query{Text: "x", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d, want 1", len(results))
	}
}

func TestNyaaSearchErrors(t *testing.T) {
	t.Run("http error", func(t *testing.T) {
		n := testNyaa(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		})
		if _, err := n.Search(context.Background(), Query{Text: "x"}); err == nil {
			t.Fatal("expected an error")
		}
	})

	t.Run("malformed xml", func(t *testing.T) {
		n := testNyaa(t, func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, "<rss><channel><item>")
		})
		if _, err := n.Latest(context.Background(), Query{Text: "x"}); err == nil {
			t.Fatal("expected a parse error")
		}
	})

	t.Run("cancelled context", func(t *testing.T) {
		n := testNyaa(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, feedXML) })
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := n.Search(ctx, Query{Text: "x"}); err == nil {
			t.Fatal("expected cancellation")
		}
	})
}

func TestParseSize(t *testing.T) {
	tests := []struct {
		in   string
		want int64
	}{
		{"1.4 GiB", 1503238553},
		{"512.0 MiB", 536870912},
		{"8.2 GiB", 8804682956},
		{"1 TiB", 1 << 40},
		{"700 MB", 700 * 1000 * 1000},
		{"1.35GB", 1_350_000_000},
		{"512.4MB", 512_400_000},
		{"1,024 MiB", 1 << 30},
		{"", 0},
		{"garbage", 0},
		{"1.4", 0},
		{"1.4 parsecs", 0},
		{"GB", 0},
	}

	for _, tt := range tests {
		if got := ParseSize(tt.in); got != tt.want {
			t.Errorf("ParseSize(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

// The RSS feed carries an infohash but no magnet link, so it has to be rebuilt
// with Nyaa's own tracker list.
func TestMagnet(t *testing.T) {
	tr := Torrent{Title: "[Group] Show - 01 [1080p]", InfoHash: "abc123"}
	magnet := tr.Magnet()

	if !strings.HasPrefix(magnet, "magnet:?xt=urn:btih:abc123") {
		t.Fatalf("magnet = %q", magnet)
	}
	if !strings.Contains(magnet, "dn=%5BGroup%5D+Show+-+01+%5B1080p%5D") {
		t.Errorf("display name not escaped: %q", magnet)
	}
	if n := strings.Count(magnet, "&tr="); n != len(trackers) {
		t.Errorf("%d trackers, want %d", n, len(trackers))
	}
	if (Torrent{}).Magnet() != "" {
		t.Error("a torrent with no infohash should produce no magnet")
	}
}

type fakeSource struct {
	name    string
	results []Torrent
	err     error
}

func (f fakeSource) Name() string { return f.name }
func (f fakeSource) Search(context.Context, Query) ([]Torrent, error) {
	return f.results, f.err
}

func TestMultiDedupesByInfoHash(t *testing.T) {
	m := Multi{Sources: []Source{
		fakeSource{name: "a", results: []Torrent{
			{InfoHash: "aaa", Seeders: 5, SeedersKnown: true},
			{InfoHash: "bbb", Seeders: 1, SeedersKnown: true},
		}},
		fakeSource{name: "b", results: []Torrent{
			{InfoHash: "AAA", Seeders: 50, SeedersKnown: true},
			{InfoHash: "ccc", Seeders: 2, SeedersKnown: true},
		}},
	}}

	got, err := m.Search(context.Background(), Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d results, want 3 after dedupe", len(got))
	}
	// Case differs between indexers; the healthier peer count wins.
	if got[0].Seeders != 50 {
		t.Errorf("seeders = %d, want the higher count kept", got[0].Seeders)
	}
}

// Nyaa's counts win where both have the torrent; TokyoTosho's silence stays
// silence where only it does.
func TestMultiPrefersCountedPeersOverNone(t *testing.T) {
	m := Multi{Sources: []Source{
		fakeSource{name: "tokyotosho", results: []Torrent{
			{InfoHash: "aaa"}, {InfoHash: "ddd"},
		}},
		fakeSource{name: "nyaa", results: []Torrent{
			{InfoHash: "aaa", Seeders: 42, Leechers: 3, SeedersKnown: true},
		}},
	}}

	got, err := m.Search(context.Background(), Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d results, want 2", len(got))
	}
	if !got[0].SeedersKnown || got[0].Seeders != 42 || got[0].Leechers != 3 {
		t.Errorf("shared torrent = %+v, want the counted record adopted", got[0])
	}
	if got[1].SeedersKnown || got[1].Seeders != 0 {
		t.Errorf("unshared torrent = %+v, want its count left unknown", got[1])
	}
}

// The reverse order must not let an uncounted record erase a real count.
func TestMultiKeepsCountsWhenTheSecondSourceHasNone(t *testing.T) {
	m := Multi{Sources: []Source{
		fakeSource{name: "nyaa", results: []Torrent{
			{InfoHash: "aaa", Seeders: 7, SeedersKnown: true},
		}},
		fakeSource{name: "tokyotosho", results: []Torrent{{InfoHash: "AAA"}}},
	}}

	got, err := m.Search(context.Background(), Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Seeders != 7 || !got[0].SeedersKnown {
		t.Fatalf("got %+v, want the counted record kept", got)
	}
}

func TestMultiToleratesFailingSource(t *testing.T) {
	m := Multi{Sources: []Source{
		fakeSource{name: "broken", err: io.ErrUnexpectedEOF},
		fakeSource{name: "ok", results: []Torrent{{InfoHash: "aaa"}}},
	}}

	got, err := m.Search(context.Background(), Query{})
	if err != nil {
		t.Fatalf("one dead source should not fail the search: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d results", len(got))
	}
}

func TestMultiReportsErrorWhenAllFail(t *testing.T) {
	m := Multi{Sources: []Source{
		fakeSource{name: "a", err: io.ErrUnexpectedEOF},
		fakeSource{name: "b", err: io.ErrUnexpectedEOF},
	}}

	if _, err := m.Search(context.Background(), Query{}); err == nil {
		t.Fatal("expected an error when every source fails")
	}
}
