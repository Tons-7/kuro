// A local swarm for the browser checks: seeds copies of the test episode (one torrent each), dials the
// throwaway kuro's engine, and answers as a nyaa site listing them. One JSON line once ready, then runs.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"html"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	atorrent "github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
)

type seeded struct {
	Episode  int    `json:"episode"`
	InfoHash string `json:"infoHash"`
	Title    string `json:"title"`
	Size     int64  `json:"size"`
}

func main() {
	src := flag.String("src", "", "episode file to seed copies of")
	dir := flag.String("dir", "", "where the copies live")
	title := flag.String("title", "Kuro Test Show", "the show the releases name")
	count := flag.Int("count", 3, "how many episodes")
	listen := flag.Int("listen", 0, "peer port to seed on")
	peer := flag.String("peer", "", "host:port of the kuro engine to dial")
	httpAddr := flag.String("http", "", "address to answer indexer searches on")
	flag.Parse()
	if *src == "" || *dir == "" || *listen == 0 || *peer == "" || *httpAddr == "" {
		fmt.Fprintln(os.Stderr, "usage: e2e-swarm -src episode.mkv -dir swarm -title Show -listen 5341 -peer 127.0.0.1:4341 -http 127.0.0.1:6399")
		os.Exit(2)
	}
	if err := run(*src, *dir, *title, *count, *listen, *peer, *httpAddr); err != nil {
		log.Fatal(err)
	}
}

func run(src, dir, title string, count, listen int, peer, httpAddr string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	cfg := atorrent.NewDefaultClientConfig()
	cfg.DataDir = dir
	cfg.Seed = true
	cfg.NoDHT = true
	cfg.DisableTrackers = true
	cfg.DisablePEX = true
	cfg.DisableIPv6 = true
	cfg.NoDefaultPortForwarding = true
	cfg.ListenPort = listen
	cl, err := atorrent.NewClient(cfg)
	if err != nil {
		return fmt.Errorf("seeder: %w", err)
	}
	defer cl.Close()

	var torrents []*atorrent.Torrent
	var listing []seeded
	for i := 1; i <= count; i++ {
		name := fmt.Sprintf("[E2E] %s - %02d [1080p]", title, i)
		path := filepath.Join(dir, name+".mkv")
		size, err := copyFile(src, path)
		if err != nil {
			return err
		}
		info := metainfo.Info{PieceLength: 1 << 20}
		if err := info.BuildFromFilePath(path); err != nil {
			return err
		}
		mi := metainfo.MetaInfo{InfoBytes: bencode.MustMarshal(info)}
		t, err := cl.AddTorrent(&mi)
		if err != nil {
			return err
		}
		<-t.GotInfo()
		torrents = append(torrents, t)
		listing = append(listing, seeded{Episode: i, InfoHash: mi.HashInfoBytes().HexString(), Title: name, Size: size})
	}
	// Seeding needs every piece checked first.
	for _, t := range torrents {
		for t.BytesMissing() > 0 {
			time.Sleep(200 * time.Millisecond)
		}
	}

	// The engine under test learns of this seeder by being dialled; a dial before it holds the torrent
	// fails and is forgotten, so it is repeated.
	addr, err := net.ResolveTCPAddr("tcp", peer)
	if err != nil {
		return err
	}
	go func() {
		for {
			for _, t := range torrents {
				t.AddPeers([]atorrent.PeerInfo{{Addr: addr, Trusted: true}})
			}
			time.Sleep(1500 * time.Millisecond)
		}
	}()

	site := &indexer{listing: listing}
	srv := &http.Server{Addr: httpAddr, Handler: site}
	ln, err := net.Listen("tcp", httpAddr)
	if err != nil {
		return err
	}
	go srv.Serve(ln)

	out, _ := json.Marshal(map[string]any{"ready": true, "torrents": listing, "indexer": "http://" + ln.Addr().String()})
	fmt.Println(string(out))
	select {}
}

func copyFile(src, dst string) (int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return 0, err
	}
	defer out.Close()
	return io.Copy(out, in)
}

// indexer answers like nyaa (listing and feed) with every release for any query, and counts the searches.
type indexer struct {
	listing []seeded
	mu      sync.Mutex
	queries []string // searches, by text; the feed polls (what is new, not a search) are counted apart
	feeds   int
}

func (x *indexer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/e2e/searches" {
		x.mu.Lock()
		defer x.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"searches": len(x.queries), "queries": x.queries, "feeds": x.feeds})
		return
	}
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	feed := r.URL.Query().Get("page") == "rss"
	x.mu.Lock()
	if feed {
		x.feeds++
	} else {
		x.queries = append(x.queries, r.URL.Query().Get("q"))
	}
	x.mu.Unlock()

	if feed {
		w.Header().Set("Content-Type", "application/xml")
		var b strings.Builder
		b.WriteString(`<?xml version="1.0"?><rss version="2.0" xmlns:nyaa="https://nyaa.si/xmlns/nyaa"><channel>`)
		for i, s := range x.listing {
			fmt.Fprintf(&b, `<item><title>%s</title><link>%s</link><guid>/view/%d</guid><pubDate>%s</pubDate>`+
				`<nyaa:infoHash>%s</nyaa:infoHash><nyaa:seeders>50</nyaa:seeders><nyaa:leechers>0</nyaa:leechers>`+
				`<nyaa:downloads>120</nyaa:downloads><nyaa:category>Anime - English-translated</nyaa:category>`+
				`<nyaa:size>%s</nyaa:size><nyaa:trusted>No</nyaa:trusted><nyaa:remake>No</nyaa:remake></item>`,
				html.EscapeString(s.Title), html.EscapeString(magnet(s)), i+1, time.Now().UTC().Format(time.RFC1123),
				s.InfoHash, size(s.Size))
		}
		b.WriteString(`</channel></rss>`)
		io.WriteString(w, b.String())
		return
	}

	w.Header().Set("Content-Type", "text/html")
	var b strings.Builder
	b.WriteString(`<html><body><table class="torrent-list"><tbody>`)
	for i, s := range x.listing {
		t := html.EscapeString(s.Title)
		fmt.Fprintf(&b, `<tr class="default"><td><a href="/?c=1_2" title="Anime - English-translated"></a></td>`+
			`<td><a href="/view/%d" title="%s">%s</a></td><td><a href="%s"><i></i></a></td><td>%s</td>`+
			`<td data-timestamp="%d">now</td><td>50</td><td>0</td><td>120</td></tr>`,
			i+1, t, t, html.EscapeString(magnet(s)), size(s.Size), time.Now().Unix())
	}
	b.WriteString(`</tbody></table></body></html>`)
	io.WriteString(w, b.String())
}

func magnet(s seeded) string {
	return "magnet:?xt=urn:btih:" + s.InfoHash + "&dn=" + strings.ReplaceAll(s.Title, " ", "+")
}

func size(bytes int64) string { return fmt.Sprintf("%.1f MiB", float64(bytes)/(1<<20)) }
