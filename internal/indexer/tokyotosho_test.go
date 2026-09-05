package indexer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// A trimmed copy of a real rss.php response: hash in base32 inside the
// description, size without a space, no peer counts anywhere.
const tokyoFeedXML = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:atom="http://www.w3.org/2005/Atom"><channel>
<title>SubsPlease - Tokyo Toshokan</title>
<item>
<category>Anime</category>
<title>[SubsPlease] Tefuda ga Oome no Victoria - 09 (1080p) [571DA8F8].mkv</title>
<link><![CDATA[https://feed.example/view/2154867/torrent]]></link>
<description><![CDATA[<a href="https://feed.example/view/2154867/torrent">Torrent Link</a><br />
<a href="magnet:?xt=urn:btih:OALRUVZ3XR6CGN6JHMX3A7RWFQPA4T35&amp;tr=http://tracker.example/announce">Magnet Link</a><br />
<a href="https://index.example/details.php?id=2107240">Tokyo Tosho</a><br />
Size: 1.35GB<br />
Authorized: Yes<br />
Submitter: subsplease<br />
Comment: Released by SubsPlease.]]></description>
<guid><![CDATA[https://index.example/details.php?id=2107240]]></guid>
<pubDate>Tue, 01 Sep 2026 15:02:33 GMT</pubDate>
</item>
<item>
<category>Anime</category>
<title>[Group] Older Show - 03 [DVD 480p].mkv</title>
<description><![CDATA[<a href="magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567">Magnet Link</a><br />
Size: 512.4MB<br />
Authorized: No<br />
Submitter: someone]]></description>
<guid><![CDATA[https://index.example/details.php?id=99]]></guid>
<pubDate>Wed, 02 Jan 2019 08:00:00 GMT</pubDate>
</item>
<item>
<category>Anime</category>
<title>Entry with no magnet at all</title>
<description><![CDATA[Size: 1.0GB<br />Authorized: No]]></description>
<guid><![CDATA[https://index.example/details.php?id=100]]></guid>
</item>
<item>
<category>Anime</category>
<title>Entry whose magnet hash is unreadable</title>
<description><![CDATA[<a href="magnet:?xt=urn:btih:11111111111111111111111111111111">Magnet Link</a><br />Size: 1.0GB]]></description>
<guid><![CDATA[https://index.example/details.php?id=101]]></guid>
</item>
</channel></rss>`

func testTokyo(t *testing.T, h http.HandlerFunc) *TokyoTosho {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return NewTokyoTosho(srv.URL, WithTokyoToshoDelay(time.Nanosecond))
}

func TestTokyoToshoParsesFeed(t *testing.T) {
	tt := testTokyo(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(tokyoFeedXML))
	})

	got, err := tt.Search(context.Background(), Query{Text: "SubsPlease"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d results, want the 2 with a readable magnet: %+v", len(got), got)
	}

	first := got[0]
	if first.Title != "[SubsPlease] Tefuda ga Oome no Victoria - 09 (1080p) [571DA8F8].mkv" {
		t.Errorf("title = %q", first.Title)
	}
	// base32 OALRUVZ3XR6CGN6JHMX3A7RWFQPA4T35 decoded to hex.
	if want := "70171a573bbc7c2337c93b2fb07e362c1e0e4f7d"; first.InfoHash != want {
		t.Errorf("infoHash = %q, want %q", first.InfoHash, want)
	}
	if first.Size != 1_350_000_000 {
		t.Errorf("size = %d, want 1.35GB in bytes", first.Size)
	}
	if !first.Trusted {
		t.Error("Authorized: Yes should mark the release trusted")
	}
	if first.Indexer != "tokyotosho" {
		t.Errorf("indexer = %q", first.Indexer)
	}
	if first.Link != "https://index.example/details.php?id=2107240" {
		t.Errorf("link = %q, want the details page", first.Link)
	}
	if first.Published.IsZero() || first.Published.Year() != 2026 {
		t.Errorf("published = %v", first.Published)
	}
	if first.Category != "Anime" {
		t.Errorf("category = %q", first.Category)
	}

	second := got[1]
	if second.InfoHash != "0123456789abcdef0123456789abcdef01234567" {
		t.Errorf("a hex magnet should pass through: %q", second.InfoHash)
	}
	if second.Trusted {
		t.Error("Authorized: No should not be trusted")
	}
	if second.Size != 512_400_000 {
		t.Errorf("size = %d, want 512.4MB in bytes", second.Size)
	}
}

// Nothing may present TokyoTosho's silence about peers as a dead swarm.
func TestTokyoToshoReportsSeedersUnknown(t *testing.T) {
	tt := testTokyo(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(tokyoFeedXML))
	})

	got, err := tt.Search(context.Background(), Query{})
	if err != nil {
		t.Fatal(err)
	}
	for _, torrent := range got {
		if torrent.SeedersKnown {
			t.Errorf("%q claims a known peer count", torrent.Title)
		}
		if torrent.Seeders != 0 {
			t.Errorf("%q invented %d seeders", torrent.Title, torrent.Seeders)
		}
	}
}

func TestTokyoToshoRequest(t *testing.T) {
	var got url.Values
	var agent string
	tt := testTokyo(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()
		agent = r.Header.Get("User-Agent")
		if r.URL.Path != "/rss.php" {
			t.Errorf("path = %q, want /rss.php", r.URL.Path)
		}
		w.Write([]byte(tokyoFeedXML))
	})

	if _, err := tt.Search(context.Background(), Query{Text: "Monster 2004"}); err != nil {
		t.Fatal(err)
	}
	if got.Get("terms") != "Monster 2004" {
		t.Errorf("terms = %q", got.Get("terms"))
	}
	if got.Get("filter") != tokyoAnimeCategory {
		t.Errorf("filter = %q, want the anime category", got.Get("filter"))
	}
	if agent == "" {
		t.Error("no User-Agent sent; the site answers those with an empty body")
	}
}

func TestTokyoToshoOmitsTermsWhenEmpty(t *testing.T) {
	var got url.Values
	tt := testTokyo(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()
		w.Write([]byte(tokyoFeedXML))
	})

	if _, err := tt.Search(context.Background(), Query{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["terms"]; ok {
		t.Errorf("terms sent for an empty query: %v", got)
	}
}

func TestTokyoToshoLimit(t *testing.T) {
	tt := testTokyo(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(tokyoFeedXML))
	})

	got, err := tt.Search(context.Background(), Query{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d results, want the limit honoured", len(got))
	}
}

func TestTokyoToshoErrors(t *testing.T) {
	t.Run("http status", func(t *testing.T) {
		tt := testTokyo(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
		})
		_, err := tt.Search(context.Background(), Query{Text: "x"})
		if err == nil || !strings.Contains(err.Error(), "502") {
			t.Fatalf("err = %v, want the status reported", err)
		}
	})

	t.Run("unparseable body with items", func(t *testing.T) {
		tt := testTokyo(t, func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("<rss><channel><item><title>truncated"))
		})
		_, err := tt.Search(context.Background(), Query{Text: "x"})
		if err == nil || !strings.Contains(err.Error(), "parse feed") {
			t.Fatalf("err = %v, want a parse failure", err)
		}
	})

	// What the live site answers a query with no matches: closing tags only,
	// which is not a document and used to be reported as a broken feed.
	t.Run("no matches", func(t *testing.T) {
		tt := testTokyo(t, func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("</channel>\n</rss>\n"))
		})
		got, err := tt.Search(context.Background(), Query{Text: "nothing here"})
		if err != nil || len(got) != 0 {
			t.Fatalf("got %v, %v, want no results and no error", got, err)
		}
	})

	t.Run("empty body", func(t *testing.T) {
		tt := testTokyo(t, func(w http.ResponseWriter, r *http.Request) {})
		got, err := tt.Search(context.Background(), Query{Text: "x"})
		if err != nil || len(got) != 0 {
			t.Fatalf("got %v, %v, want no results and no error", got, err)
		}
	})

	t.Run("empty channel", func(t *testing.T) {
		tt := testTokyo(t, func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`<?xml version="1.0"?><rss><channel></channel></rss>`))
		})
		got, err := tt.Search(context.Background(), Query{Text: "x"})
		if err != nil || len(got) != 0 {
			t.Fatalf("got %v, %v, want no results and no error", got, err)
		}
	})

	t.Run("cancelled context", func(t *testing.T) {
		tt := testTokyo(t, func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(tokyoFeedXML))
		})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := tt.Search(ctx, Query{Text: "x"}); err == nil {
			t.Fatal("a cancelled search should fail")
		}
	})
}

func TestTokyoToshoName(t *testing.T) {
	if got := NewTokyoTosho("http://example.test/").Name(); got != "tokyotosho" {
		t.Errorf("Name() = %q", got)
	}
}
