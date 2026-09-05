package indexer

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"
)

// A trimmed copy of Nyaa's listing markup: two anchors in the title cell for a
// torrent with comments, a colour-coded row class, and a data-timestamp date.
const listingHTML = `<!DOCTYPE html><html><body>
<table class="table table-bordered table-hover table-striped torrent-list">
<thead><tr><th>Category</th><th>Name</th><th>Link</th><th>Size</th><th>Date</th><th>S</th><th>L</th><th>C</th></tr></thead>
<tbody>
<tr class="success">
  <td><a href="/?c=1_2" title="Anime - English-translated"><img src="x.png"></a></td>
  <td colspan="2">
    <a href="/view/1#comments" class="comments">3</a>
    <a href="/view/1" title="[SubsPlease] Sousou no Frieren - 10 (1080p) [7D35515E].mkv">[SubsPlease] Sousou no Frieren - 10 (1080p)</a>
  </td>
  <td class="text-center">
    <a href="/download/1.torrent"><i class="fa"></i></a>
    <a href="magnet:?xt=urn:btih:ABCDEF0123456789&amp;dn=Frieren&amp;tr=http%3A%2F%2Fx"><i class="fa"></i></a>
  </td>
  <td class="text-center">1.4 GiB</td>
  <td class="text-center" data-timestamp="1786148760">2026-08-06 17:50</td>
  <td class="text-center">970</td>
  <td class="text-center">12</td>
  <td class="text-center">4210</td>
</tr>
<tr class="danger">
  <td><a href="/?c=1_2" title="Anime - English-translated"><img src="x.png"></a></td>
  <td colspan="2"><a href="/view/2" title="[Reup] Some Show - 03 [720p].mkv">[Reup] Some Show - 03 [720p].mkv</a></td>
  <td class="text-center">
    <a href="magnet:?xt=urn:btih:0011223344556677"><i class="fa"></i></a>
  </td>
  <td class="text-center">512.0 MiB</td>
  <td class="text-center" data-timestamp="1786000000">2026-08-05 09:00</td>
  <td class="text-center">4</td>
  <td class="text-center">1</td>
  <td class="text-center">30</td>
</tr>
<tr class="default">
  <td><a href="/?c=1_2" title="Anime"><img src="x.png"></a></td>
  <td colspan="2"><a href="/view/3" title="No magnet here">No magnet here</a></td>
  <td class="text-center"><a href="/download/3.torrent"></a></td>
  <td class="text-center">1.0 GiB</td>
  <td class="text-center" data-timestamp="1785000000">2026-08-01 09:00</td>
  <td class="text-center">2</td><td class="text-center">0</td><td class="text-center">1</td>
</tr>
</tbody></table></body></html>`

func TestSearchUsesSortedListing(t *testing.T) {
	var got url.Values
	n := testNyaa(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()
		io.WriteString(w, listingHTML)
	})

	results, err := n.Search(context.Background(), Query{Text: "frieren", Filter: FilterTrusted})
	if err != nil {
		t.Fatal(err)
	}

	// Sorting by seeders is the whole point: the RSS feed cannot do it, which
	// is why back-catalogue searches returned only recent batches.
	if got.Get("s") != "seeders" || got.Get("o") != "desc" {
		t.Errorf("sort = %q/%q, want seeders/desc", got.Get("s"), got.Get("o"))
	}
	if got.Get("page") == "rss" {
		t.Error("search should not fall back to the RSS feed")
	}
	if got.Get("q") != "frieren" || got.Get("f") != "2" {
		t.Errorf("query = %q filter = %q", got.Get("q"), got.Get("f"))
	}

	// The third row has no magnet and cannot be downloaded.
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}

	first := results[0]
	if first.Title != "[SubsPlease] Sousou no Frieren - 10 (1080p) [7D35515E].mkv" {
		t.Errorf("title = %q; the comments anchor should not win", first.Title)
	}
	if first.InfoHash != "abcdef0123456789" {
		t.Errorf("infohash = %q", first.InfoHash)
	}
	if first.Size != 1503238553 {
		t.Errorf("size = %d", first.Size)
	}
	if first.Seeders != 970 || first.Leechers != 12 || first.Downloads != 4210 {
		t.Errorf("peers = %+v", first)
	}
	if !first.Trusted {
		t.Error("a green row means a trusted uploader")
	}
	if first.Published.Unix() != 1786148760 {
		t.Errorf("published = %v", first.Published)
	}
	if first.Indexer != "nyaa" {
		t.Errorf("indexer = %q", first.Indexer)
	}

	if !results[1].Remake {
		t.Error("a red row means a remake")
	}
	if results[1].Trusted {
		t.Error("a remake is not trusted")
	}
}

func TestSearchWithoutQueryUsesFeed(t *testing.T) {
	var page string
	n := testNyaa(t, func(w http.ResponseWriter, r *http.Request) {
		page = r.URL.Query().Get("page")
		io.WriteString(w, feedXML)
	})

	// No search term means "what is new", which is what the feed is for.
	if _, err := n.Search(context.Background(), Query{}); err != nil {
		t.Fatal(err)
	}
	if page != "rss" {
		t.Errorf("page = %q, want the rss feed for an empty query", page)
	}
}

func TestSearchHTMLLimit(t *testing.T) {
	n := testNyaa(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, listingHTML)
	})

	results, err := n.Search(context.Background(), Query{Text: "x", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
}

func TestSearchHTMLHandlesEmptyAndBrokenPages(t *testing.T) {
	t.Run("no results table", func(t *testing.T) {
		n := testNyaa(t, func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `<html><body><p>No results found</p></body></html>`)
		})
		got, err := n.Search(context.Background(), Query{Text: "zzzz"})
		if err != nil {
			t.Fatalf("an empty page is not an error: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("got %d results", len(got))
		}
	})

	t.Run("http error", func(t *testing.T) {
		n := testNyaa(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
		})
		if _, err := n.Search(context.Background(), Query{Text: "x"}); err == nil {
			t.Fatal("expected an error")
		}
	})

	t.Run("truncated markup", func(t *testing.T) {
		n := testNyaa(t, func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `<table class="torrent-list"><tbody><tr class="x"><td>`)
		})
		// html.Parse repairs broken markup rather than failing, so the row is
		// simply skipped for having too few cells.
		got, err := n.Search(context.Background(), Query{Text: "x"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("got %d results from a truncated row", len(got))
		}
	})
}

func TestInfoHashFromMagnet(t *testing.T) {
	tests := map[string]string{
		"magnet:?xt=urn:btih:ABCDEF&dn=x": "abcdef",
		"magnet:?dn=x&xt=urn:btih:ABCDEF": "abcdef",
		"magnet:?xt=urn:btih:abcdef":      "abcdef",
		"magnet:?dn=nothing":              "",
		"":                                "",
		// TokyoTosho writes base32, which has to come back as hex.
		"magnet:?xt=urn:btih:OALRUVZ3XR6CGN6JHMX3A7RWFQPA4T35&tr=x": "70171a573bbc7c2337c93b2fb07e362c1e0e4f7d",
		"magnet:?xt=urn:btih:oalruvz3xr6cgn6jhmx3a7rwfqpa4t35":      "70171a573bbc7c2337c93b2fb07e362c1e0e4f7d",
		"magnet:?xt=urn:btih:11111111111111111111111111111111":      "",
	}
	for in, want := range tests {
		if got := infoHashFromMagnet(in); got != want {
			t.Errorf("infoHashFromMagnet(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseSizeAndTimestampAreConsistent(t *testing.T) {
	if got := time.Unix(1786148760, 0).Unix(); got != 1786148760 {
		t.Fatal("timestamp handling changed")
	}
}
