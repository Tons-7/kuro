package engine

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	atorrent "github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"

	"kuro/internal/torrent"
)

// pack is three episodes whose sizes are not piece multiples, so episode 2
// starts mid-piece, as in a real season pack.
func makePack(t *testing.T, dir string) (metainfo.MetaInfo, map[string][]byte) {
	t.Helper()
	root := filepath.Join(dir, "Show Pack")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	contents := map[string][]byte{}
	for i, size := range []int{300_001, 250_003, 200_005} {
		b := make([]byte, size)
		rand.Read(b)
		name := fmt.Sprintf("Show - %02d.mkv", i+1)
		contents[name] = b
		if err := os.WriteFile(filepath.Join(root, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	info := metainfo.Info{PieceLength: 64 << 10}
	if err := info.BuildFromFilePath(root); err != nil {
		t.Fatal(err)
	}
	var mi metainfo.MetaInfo
	var err error
	if mi.InfoBytes, err = bencode.Marshal(info); err != nil {
		t.Fatal(err)
	}
	return mi, contents
}

func TestMain(m *testing.M) {
	janitorEvery, stallAfter = 500*time.Millisecond, 2*time.Second
	os.Exit(m.Run())
}

func quietConfig(cfg *atorrent.ClientConfig) {
	cfg.ListenPort = freePort()
	cfg.NoDHT = true
	cfg.DisableTrackers = true
	cfg.DisablePEX = true
	cfg.DisableIPv6 = true
	cfg.NoDefaultPortForwarding = true
}

// seeder serves the pack from dir.
func seeder(t *testing.T, dir string, mi metainfo.MetaInfo) *atorrent.Client {
	t.Helper()
	var cl *atorrent.Client
	var err error
	// Retried: a random port can land in a range Windows reserves.
	for range 5 {
		cfg := atorrent.NewDefaultClientConfig()
		quietConfig(cfg)
		cfg.DataDir = dir
		cfg.DefaultStorage = files{base: dir, pieces: storage.NewMapPieceCompletion()}
		cfg.Seed = true
		if cl, err = atorrent.NewClient(cfg); err == nil {
			break
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cl.Close() })
	tt, err := cl.AddTorrent(&mi)
	if err != nil {
		t.Fatal(err)
	}
	if err := tt.VerifyDataContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	return cl
}

type fixture struct {
	engine  *Engine
	client  *torrent.Client
	magnet  string
	content map[string][]byte
	cache   string
	session string
	legacy  []string
	peer    *net.TCPAddr
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	seedDir := t.TempDir()
	mi, content := makePack(t, seedDir)
	s := seeder(t, seedDir, mi)
	f := &fixture{
		content: content,
		cache:   t.TempDir(),
		session: t.TempDir(),
		peer:    &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: s.LocalPort()},
		magnet:  mi.Magnet(nil, nil).String(),
	}
	f.start(t)
	return f
}

func (f *fixture) start(t *testing.T) {
	t.Helper()
	e, err := New(Options{CacheDir: f.cache, SessionDir: f.session, LegacySessions: f.legacy},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	e.configure = quietConfig
	e.onNew = func(tt *atorrent.Torrent) {
		tt.AddPeers([]atorrent.PeerInfo{{Addr: f.peer, Trusted: true}})
	}
	t.Cleanup(e.Stop)
	f.engine, f.client = e, e.Client()
}

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return c
}

func readRange(t *testing.T, url string, from, to int64) []byte {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", from, to))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusPartialContent {
		t.Fatalf("range read: %s", res.Status)
	}
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The calls playback makes, in its order: inspect, pick, add one file, stream.
func TestPlaybackPathOverTheEngine(t *testing.T) {
	f := newFixture(t)
	c := ctx(t)

	inspected, err := f.client.Inspect(c, f.magnet)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if len(inspected.Details.Files) != 3 {
		t.Fatalf("files = %+v", inspected.Details.Files)
	}
	if live, _ := f.client.Live(c); len(live) != 0 {
		t.Errorf("an inspected torrent was listed as downloading: %v", live)
	}

	file, idx, ok := torrent.PickEpisode(inspected.Details.Files, 2)
	if !ok || idx != 1 {
		t.Fatalf("picked %d %v", idx, ok)
	}
	added, err := f.client.Add(c, f.magnet, file)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := f.client.WaitLive(c, added.ID, 20*time.Second); err != nil {
		t.Fatal(err)
	}

	// Episode 2 starts mid-piece; its head and a seek must read exactly.
	want := f.content["Show - 02.mkv"]
	url := f.client.StreamURL(added.ID, idx)
	if got := readRange(t, url, 0, 9_999); !bytes.Equal(got, want[:10_000]) {
		t.Fatal("head bytes differ")
	}
	if got := readRange(t, url, 200_000, int64(len(want))-1); !bytes.Equal(got, want[200_000:]) {
		t.Fatal("seek bytes differ")
	}
	if err := f.client.PrewarmHead(c, added.ID, idx, 64<<10); err != nil {
		t.Fatalf("prewarm: %v", err)
	}

	if err := f.client.Await(c, added.ID, 10*time.Second); err != nil {
		t.Fatalf("await: %v", err)
	}
	stats, _ := f.client.Stats(c, added.ID)
	if !stats.Finished || stats.TotalBytes != int64(len(want)) {
		t.Fatalf("stats = %+v", stats)
	}
	// Only the piece it shares with episode 2, not the episode.
	if stats.FileProgress[0] >= 64<<10 || stats.FileProgress[2] >= 64<<10 {
		t.Errorf("an unselected episode was downloaded: %v", stats.FileProgress)
	}
	disk, err := os.ReadFile(filepath.Join(f.cache, "Show Pack", "Show - 02.mkv"))
	if err != nil || !bytes.Equal(disk, want) {
		t.Fatalf("file on disk: %v", err)
	}
	d, _ := f.client.Details(c, added.ID)
	if d.OutputFolder != filepath.Join(f.cache, "Show Pack") || !d.Files[1].Included || d.Files[0].Included {
		t.Fatalf("details = %+v", d)
	}
}

// The next episode of a held pack joins its selection; the first stays.
func TestNextEpisodeOfHeldPack(t *testing.T) {
	f := newFixture(t)
	c := ctx(t)
	in, err := f.client.Inspect(c, f.magnet)
	if err != nil {
		t.Fatal(err)
	}
	first, err := f.client.Add(c, f.magnet, in.Details.Files[0])
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.client.Add(c, f.magnet, in.Details.Files[1])
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("one torrent became two: %d, %d", first.ID, second.ID)
	}
	d, _ := f.client.Details(c, first.ID)
	var on []int
	for i, file := range d.Files {
		if file.Included {
			on = append(on, i)
		}
	}
	if !slices.Equal(on, []int{0, 1}) {
		t.Fatalf("selection = %v", on)
	}
}

func TestPauseStartAndRemove(t *testing.T) {
	f := newFixture(t)
	c := ctx(t)
	in, _ := f.client.Inspect(c, f.magnet)
	added, err := f.client.Add(c, f.magnet, in.Details.Files[2])
	if err != nil {
		t.Fatal(err)
	}
	if err := f.client.Pause(c, added.ID); err != nil {
		t.Fatal(err)
	}
	if s, _ := f.client.Stats(c, added.ID); s.State != "paused" {
		t.Fatalf("state %q after pause", s.State)
	}
	if err := f.client.Await(c, added.ID, time.Minute); err != torrent.ErrPaused {
		t.Fatalf("await on paused = %v", err)
	}
	if err := f.client.Start(c, added.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.client.Await(c, added.ID, 10*time.Second); err != nil {
		s, _ := f.client.Stats(c, added.ID)
		en, _ := f.engine.entryByID(added.ID)
		st := en.t.Stats()
		var status bytes.Buffer
		f.engine.cl.WriteStatus(&status)
		t.Fatalf("await after start: %v (state %s, peers %d, conns %d, half-open %d, pending peers %d)\n%s",
			err, s.State, s.Peers(), st.ActivePeers, st.HalfOpenPeers, st.PendingPeers, status.String())
	}

	path := filepath.Join(f.cache, "Show Pack", "Show - 03.mkv")
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	if err := f.client.Delete(c, added.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("delete left the file")
	}
	if _, err := os.Stat(filepath.Join(f.cache, "Show Pack")); !os.IsNotExist(err) {
		t.Error("delete left the empty pack folder")
	}
	if live, _ := f.client.Live(c); len(live) != 0 {
		t.Errorf("still listed: %v", live)
	}
}

// A restart keeps the torrent, its selection and what was downloaded.
func TestRestartKeepsTheSession(t *testing.T) {
	f := newFixture(t)
	c := ctx(t)
	in, _ := f.client.Inspect(c, f.magnet)
	added, err := f.client.Add(c, f.magnet, in.Details.Files[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := f.client.Await(c, added.ID, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	f.engine.Stop()

	// No peer this time: everything must come from disk.
	f.peer = &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1}
	f.start(t)
	live, err := f.client.Live(c)
	if err != nil || len(live) != 1 {
		t.Fatalf("after restart: %v %v", live, err)
	}
	for _, id := range live {
		if err := f.client.WaitLive(c, id, 10*time.Second); err != nil {
			t.Fatal(err)
		}
		s, _ := f.client.Stats(c, id)
		if !s.Finished {
			t.Fatalf("restored torrent not complete: %+v", s)
		}
		d, _ := f.client.Details(c, id)
		if !d.Files[0].Included || d.Files[1].Included {
			t.Fatalf("selection lost: %+v", d.Files)
		}
	}
}

// rqbit's downloads carry over: its session is read, the data on disk is
// hashed, and nothing is fetched again. A session rqbit keeps outside the
// cache is found when kuro's own is missing, and left alone.
func TestCarriesOverRqbitDownloads(t *testing.T) {
	seedDir := t.TempDir()
	mi, content := makePack(t, seedDir)
	cache, legacy := t.TempDir(), t.TempDir()
	missing := filepath.Join(cache, "rqbit-session")

	// rqbit had episode 1 downloaded into the cache, with the start of episode 2
	// that its last piece spills into, as rqbit writes it.
	pack := filepath.Join(cache, "Show Pack")
	os.MkdirAll(pack, 0o755)
	os.WriteFile(filepath.Join(pack, "Show - 01.mkv"), content["Show - 01.mkv"], 0o644)
	os.WriteFile(filepath.Join(pack, "Show - 02.mkv"), content["Show - 02.mkv"][:64<<10], 0o644)
	info, _ := mi.UnmarshalInfo()
	hash := mi.HashInfoBytes().HexString()
	var buf bytes.Buffer
	mi.Write(&buf)
	os.WriteFile(filepath.Join(legacy, hash+".torrent"), buf.Bytes(), 0o644)
	doc, _ := json.Marshal(map[string]any{"torrents": map[string]any{
		"0": map[string]any{"info_hash": hash, "output_folder": pack, "only_files": []int{0}, "is_paused": false},
		// A damaged record is skipped, not fatal.
		"1": map[string]any{"info_hash": "not-hex", "output_folder": pack, "only_files": []int{0}},
	}})
	os.WriteFile(filepath.Join(legacy, "session.json"), doc, 0o644)

	f := &fixture{cache: cache, session: t.TempDir(), legacy: []string{missing, legacy},
		peer: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1}}
	f.start(t)
	c := ctx(t)
	live, err := f.client.Live(c)
	if err != nil || len(live) != 1 {
		t.Fatalf("carried over: %v %v", live, err)
	}
	id := live[hash]
	if err := f.client.WaitLive(c, id, 20*time.Second); err != nil {
		t.Fatal(err)
	}
	s, _ := f.client.Stats(c, id)
	if !s.Finished || s.TotalBytes != info.Files[0].Length {
		t.Fatalf("carried-over episode not recognised as complete: %+v", s)
	}
	if _, err := os.Stat(filepath.Join(legacy, "session.json")); err != nil {
		t.Error("rqbit's session was touched")
	}
}

// kuro's own rqbit session, in the cache, goes once the engine's holds it all.
func TestImportedSessionInTheCacheIsRemoved(t *testing.T) {
	seedDir := t.TempDir()
	mi, content := makePack(t, seedDir)
	cache := t.TempDir()
	legacy := filepath.Join(cache, "rqbit-session")
	os.MkdirAll(legacy, 0o755)

	pack := filepath.Join(cache, "Show Pack")
	os.MkdirAll(pack, 0o755)
	os.WriteFile(filepath.Join(pack, "Show - 01.mkv"), content["Show - 01.mkv"], 0o644)
	hash := mi.HashInfoBytes().HexString()
	var buf bytes.Buffer
	mi.Write(&buf)
	os.WriteFile(filepath.Join(legacy, hash+".torrent"), buf.Bytes(), 0o644)
	doc, _ := json.Marshal(map[string]any{"torrents": map[string]any{
		"0": map[string]any{"info_hash": hash, "output_folder": pack, "only_files": []int{0}},
	}})
	os.WriteFile(filepath.Join(legacy, "session.json"), doc, 0o644)

	session := t.TempDir()
	f := &fixture{cache: cache, session: session, legacy: []string{legacy},
		peer: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1}}
	f.start(t)
	live, err := f.client.Live(ctx(t))
	if err != nil || len(live) != 1 {
		t.Fatalf("carried over: %v %v", live, err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Errorf("the imported session is still there: %v", err)
	}
	if _, err := os.Stat(filepath.Join(session, hash+".torrent")); err != nil {
		t.Errorf("metainfo not kept: %v", err)
	}
}
