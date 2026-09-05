package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"kuro/internal/config"
	"kuro/internal/indexer"
	"kuro/internal/library"
	"kuro/internal/mal"
	"kuro/internal/store"
	"kuro/internal/torrent"
)

// aniRecorder is an AniList fake that answers reads with a fixed body and keeps
// every mutation's variables.
type aniRecorder struct {
	body string

	mu    sync.Mutex
	saves []map[string]any
}

func (a *aniRecorder) handler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if strings.Contains(req.Query, "SaveMediaListEntry") {
		a.mu.Lock()
		a.saves = append(a.saves, req.Variables)
		a.mu.Unlock()
		io.WriteString(w, `{"data":{"SaveMediaListEntry":{"id":9,"mediaId":1,"updatedAt":5}}}`)
		return
	}
	io.WriteString(w, a.body)
}

func (a *aniRecorder) awaitSaves(n int) []map[string]any {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		a.mu.Lock()
		got := append([]map[string]any(nil), a.saves...)
		a.mu.Unlock()
		if len(got) >= n {
			return got
		}
		time.Sleep(20 * time.Millisecond)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]map[string]any(nil), a.saves...)
}

func withSync(d *Deps) {
	d.AniList.SetToken("tok")
	d.Sync = library.NewSync(d.Store, d.AniList, d.Log)
}

// Hand edits must reach the trackers behind the response, not on the next poll.
func TestHandEditsArePushedAtOnce(t *testing.T) {
	ani := &aniRecorder{body: `{"data":{}}`}
	h := newHarness(t, config.Config{}, ani.handler, withSync)
	seedShow(t, h, 1, 12)

	h.postJSON(t, "/api/watched", map[string]any{"animeId": 1, "episode": 4})
	saves := ani.awaitSaves(1)
	if len(saves) != 1 || saves[0]["progress"] != float64(4) {
		t.Fatalf("mark watched pushed %v", saves)
	}

	h.postJSON(t, "/api/watched", map[string]any{"animeId": 1, "episode": 3, "watched": false})
	saves = ani.awaitSaves(2)
	if len(saves) != 2 || saves[1]["progress"] != float64(2) {
		t.Fatalf("unwatch pushed %v", saves)
	}

	h.postJSON(t, "/api/score", map[string]any{"animeId": 1, "score": 80})
	saves = ani.awaitSaves(3)
	if len(saves) != 3 || saves[2]["scoreRaw"] != float64(80) {
		t.Fatalf("score pushed %v", saves)
	}

	h.postJSON(t, "/api/status", map[string]any{"animeId": 1, "status": "PAUSED"})
	saves = ani.awaitSaves(4)
	if len(saves) != 4 || saves[3]["status"] != "PAUSED" {
		t.Fatalf("status pushed %v", saves)
	}
	if dirty, _ := h.store.DirtyEntries(context.Background(), 0); len(dirty) != 0 {
		t.Fatalf("still dirty after push: %+v", dirty)
	}
}

// The picker lists what the finder ranked, hash included, for the play call.
func TestEpisodeSourcesListsRankedReleases(t *testing.T) {
	h := newHarness(t, config.Config{}, nil, func(d *Deps) {
		d.Finder = library.NewFinder(d.Store, d.Indexer, d.Log)
	})
	seedShow(t, h, 1, 12)
	h.indexer.results = []indexer.Torrent{
		{Title: "[Good] Show - 01 [1080p].mkv", InfoHash: "AAAA", Size: 900 << 20, Seeders: 40},
		{Title: "[Meh] Show - 01 [480p].mkv", InfoHash: "BBBB", Size: 200 << 20, Seeders: 2},
		{Title: "[Wrong] Show - 02 [1080p].mkv", InfoHash: "CCCC", Size: 900 << 20, Seeders: 99},
	}

	res, body := h.do(t, "GET", "/api/episode/sources?id=1&episode=1")
	if res.StatusCode != 200 {
		t.Fatalf("HTTP %d %v", res.StatusCode, body)
	}
	results, _ := body["results"].([]any)
	if len(results) != 2 {
		t.Fatalf("results = %v, want the two episode-1 releases", body)
	}
	first := results[0].(map[string]any)
	if first["Torrent"].(map[string]any)["infoHash"] != "AAAA" || first["autoPick"] != true {
		t.Fatalf("best = %v", first)
	}
	if best, _ := body["best"].(map[string]any); best == nil {
		t.Fatal("no best release reported")
	}

	res, _ = h.do(t, "GET", "/api/episode/sources?episode=1")
	if res.StatusCode != 400 {
		t.Fatalf("missing id: HTTP %d", res.StatusCode)
	}
}

func TestEpisodeSourcesWithoutFinder(t *testing.T) {
	h := newHarness(t, config.Config{}, nil)
	if res, _ := h.do(t, "GET", "/api/episode/sources?id=1&episode=1"); res.StatusCode != 503 {
		t.Fatalf("HTTP %d, want 503", res.StatusCode)
	}
}

// The downloads list carries the engine's live view: a torrent being
// re-checked after a launch says so rather than reading as paused.
func TestDownloadsReportCheckingAndEpisodes(t *testing.T) {
	rqbit := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/torrents":
			io.WriteString(w, `{"torrents":[{"id":1,"info_hash":"PACK","name":"pack"},{"id":2,"info_hash":"SOLO","name":"solo"}]}`)
		case strings.Contains(r.URL.Path, "/1/stats"):
			io.WriteString(w, `{"state":"initializing","finished":false,"progress_bytes":100,"total_bytes":1000}`)
		case strings.Contains(r.URL.Path, "/2/stats"):
			io.WriteString(w, `{"state":"paused","finished":false,"progress_bytes":500,"total_bytes":1000}`)
		}
	}))
	t.Cleanup(rqbit.Close)

	h := newHarness(t, config.Config{}, nil, func(d *Deps) {
		d.Cache = library.NewCache(d.Store, torrent.NewClient(rqbit.URL), t.TempDir(), d.Log)
	})
	ctx := context.Background()
	seedShow(t, h, 1, 12)
	for _, r := range []store.TorrentRecord{
		{InfoHash: "pack", RqbitID: 1, Name: "pack", TotalSize: 1000, AnimeID: 1, EpKey: "5", FileIndex: 1, FilePath: "5.mkv"},
		{InfoHash: "pack", RqbitID: 1, Name: "pack", TotalSize: 1000, AnimeID: 1, EpKey: "4", FileIndex: 0, FilePath: "4.mkv"},
		{InfoHash: "solo", RqbitID: 2, Name: "solo", TotalSize: 1000, AnimeID: 1, EpKey: "7", FileIndex: 0, FilePath: "7.mkv"},
	} {
		if err := h.store.RecordTorrent(ctx, r); err != nil {
			t.Fatal(err)
		}
	}

	_, body := h.do(t, "GET", "/api/downloads")
	items, _ := body["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("items = %v", body)
	}
	byHash := map[string]map[string]any{}
	for _, it := range items {
		m := it.(map[string]any)
		byHash[m["infoHash"].(string)] = m
	}
	pack, solo := byHash["pack"], byHash["solo"]
	if pack["checking"] != true || pack["paused"] != false || pack["episode"] != "4, 5" {
		t.Fatalf("pack = %v", pack)
	}
	if eps, _ := pack["episodes"].([]any); len(eps) != 2 || eps[0] != "4" {
		t.Fatalf("pack episodes = %v", pack["episodes"])
	}
	if pack["percent"] != float64(10) {
		t.Fatalf("pack percent = %v, want the engine's 10", pack["percent"])
	}
	if solo["checking"] != false || solo["paused"] != true || solo["percent"] != float64(50) {
		t.Fatalf("solo = %v", solo)
	}
}

// Random without genres draws from the corpus and resolves the card upstream;
// with nothing in the corpus (a fresh install still seeding) it asks AniList.
func TestRandomDrawsFromTheCorpusThenAniList(t *testing.T) {
	var randomQueries int
	h := newHarness(t, config.Config{}, func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query string `json:"query"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		if strings.Contains(req.Query, "query Random") {
			randomQueries++
			io.WriteString(w, `{"data":{"Page":{"pageInfo":{"total":1},"media":[{"id":66,"title":{"romaji":"Upstream pick"}}]}}}`)
			return
		}
		io.WriteString(w, `{"data":{"Page":{"media":[{"id":55,"title":{"romaji":"Corpus pick"},"format":"TV"}]}}}`)
	})
	if _, err := h.store.SaveCorpus(context.Background(), []store.CorpusEntry{
		{AniListID: 55, Kind: "TV", Year: 2020, Titles: []store.CorpusTitle{{Text: "Corpus pick", Kind: "primary"}}},
	}); err != nil {
		t.Fatal(err)
	}

	res, body := h.do(t, "GET", "/api/random?format=TV&year=2020")
	if res.StatusCode != 200 || body["id"] != float64(55) || randomQueries != 0 {
		t.Fatalf("HTTP %d %v (upstream random calls %d)", res.StatusCode, body, randomQueries)
	}
	res, body = h.do(t, "GET", "/api/random?year=1999")
	if res.StatusCode != 200 || body["id"] != float64(66) || randomQueries != 1 {
		t.Fatalf("fallback: HTTP %d %v (upstream random calls %d)", res.StatusCode, body, randomQueries)
	}
}

func TestRandomWithGenresSurfacesUpstreamFailure(t *testing.T) {
	h := newHarness(t, config.Config{}, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"data":{"Page":{"pageInfo":{"total":0},"media":[]}}}`)
	})
	if res, _ := h.do(t, "GET", "/api/random?genres=Nothing"); res.StatusCode != 502 {
		t.Fatalf("HTTP %d, want 502", res.StatusCode)
	}
}

func TestMALImportPullsTheList(t *testing.T) {
	malSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"data":[{"node":{"id":777,"title":"Show","num_episodes":12},
		  "list_status":{"status":"watching","score":7,"num_episodes_watched":5,
		                 "updated_at":"2026-08-01T00:00:00+00:00"}}],"paging":{}}`)
	}))
	t.Cleanup(malSrv.Close)

	h := newHarness(t, config.Config{}, nil, func(d *Deps) {
		c := mal.New(d.Log, mal.WithAPI(malSrv.URL), mal.WithOAuthBase(malSrv.URL),
			mal.WithCredentials("id", ""), mal.WithoutRateLimit())
		c.SetToken(mal.Token{Access: "acc", Expires: time.Now().Add(time.Hour)})
		d.MAL, d.MALSync = c, library.NewMALSync(d.Store, c, d.Log)
	})
	ctx := context.Background()
	malID := 777
	if _, err := h.store.ImportList(ctx, []store.Anime{{ID: 1, Romaji: "Show", MalID: &malID}}, nil, store.ImportMerge); err != nil {
		t.Fatal(err)
	}

	res, body := h.do(t, "POST", "/api/mal/import")
	if res.StatusCode != 200 || body["matched"] != float64(1) || body["applied"] != float64(1) {
		t.Fatalf("HTTP %d %v", res.StatusCode, body)
	}
	if e, _ := h.store.ListEntry(ctx, 1); e.Progress != 5 || e.Score != 70 {
		t.Fatalf("entry = %+v", e)
	}
}

func TestMALImportRequiresAConnection(t *testing.T) {
	h := newHarness(t, config.Config{}, nil)
	if res, _ := h.do(t, "POST", "/api/mal/import"); res.StatusCode != 412 {
		t.Fatalf("HTTP %d, want 412", res.StatusCode)
	}
}

func TestBookmarkRejectsMissingAnime(t *testing.T) {
	h := newHarness(t, config.Config{}, nil)
	if res, _ := h.postJSON(t, "/api/bookmarks", map[string]any{"favourite": true}); res.StatusCode != 400 {
		t.Fatalf("HTTP %d, want 400", res.StatusCode)
	}
}

// A MAL-only show (negative id) can be tagged, ticked, rated and bookmarked.
func TestHandlersAcceptMALOnlyIDs(t *testing.T) {
	ani := &aniRecorder{body: `{"data":{}}`}
	h := newHarness(t, config.Config{}, ani.handler, withSync)
	ctx := context.Background()
	if _, err := h.store.SaveCorpus(ctx, []store.CorpusEntry{{AniListID: -900, MalID: 900, Kind: "TV", Episodes: 12,
		Titles: []store.CorpusTitle{{Text: "MAL only", Kind: "primary"}}}}); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		path string
		body map[string]any
	}{
		{"/api/status", map[string]any{"animeId": -900, "status": "CURRENT"}},
		{"/api/watched", map[string]any{"animeId": -900, "episode": 4}},
		{"/api/watched", map[string]any{"animeId": -900, "episode": 4, "watched": false}},
		{"/api/score", map[string]any{"animeId": -900, "score": 70}},
		{"/api/bookmarks", map[string]any{"animeId": -900, "favourite": true}},
	} {
		if res, body := h.postJSON(t, c.path, c.body); res.StatusCode != 200 {
			t.Fatalf("%s %v: HTTP %d %v", c.path, c.body, res.StatusCode, body)
		}
	}
	e, _ := h.store.ListEntry(ctx, -900)
	if e.Progress != 3 || e.Score != 70 || e.Status != "CURRENT" {
		t.Fatalf("entry = %+v", e)
	}
	if b, _ := h.store.Bookmark(ctx, -900); !b.Favourite {
		t.Fatal("bookmark not stored")
	}
	// Nothing goes to AniList for it.
	time.Sleep(200 * time.Millisecond)
	if saves := ani.awaitSaves(0); len(saves) != 0 {
		t.Fatalf("pushed to AniList: %v", saves)
	}
	if res, _ := h.do(t, "GET", "/api/episodes?id=-900"); res.StatusCode != 200 {
		t.Fatalf("episodes: HTTP %d", res.StatusCode)
	}
}
