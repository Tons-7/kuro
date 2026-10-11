package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"kuro/internal/config"
	"kuro/internal/library"
	"kuro/internal/store"
	"kuro/internal/torrent"
)

// An engine holding one torrent in the cache folder, recording what it is told to do with it.
type recordingEngine struct {
	mu      sync.Mutex
	actions []string
}

func (e *recordingEngine) did(action string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Contains(e.actions, action)
}

const heldHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// downloadsHarness is a server with a queue of three episodes and one finished download on record.
func downloadsHarness(t *testing.T) (*harness, *recordingEngine) {
	t.Helper()
	dir := t.TempDir()
	eng := &recordingEngine{}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /torrents", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"torrents": []map[string]any{{"id": 1, "info_hash": heldHash, "name": "Show - 01.mkv"}},
		})
	})
	mux.HandleFunc("GET /torrents/{id}", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"info_hash": heldHash, "output_folder": filepath.Join(dir, "show")})
	})
	mux.HandleFunc("POST /torrents/{id}/{action}", func(w http.ResponseWriter, r *http.Request) {
		eng.mu.Lock()
		eng.actions = append(eng.actions, r.PathValue("action"))
		eng.mu.Unlock()
		fmt.Fprint(w, "{}")
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := newHarness(t, config.Config{}, nil)
	h.server.cache = library.NewCache(h.store, torrent.NewClient(srv.URL), dir, log)
	h.server.downloader = library.NewDownloader(h.store, library.NewPrefetcher(h.store, nil, nil, log), nil, log)

	ctx := context.Background()
	episodes := 12
	if _, err := h.store.ImportList(ctx,
		[]store.Anime{{ID: 1, Romaji: "Show", Synonyms: "[]", Genres: "[]", Episodes: &episodes}},
		nil, store.ImportMerge); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.Enqueue(ctx, 1, 1, []int{2, 3, 4}); err != nil {
		t.Fatal(err)
	}
	if err := h.store.TrackTorrent(ctx, heldHash, 1, "Show - 01.mkv"); err != nil {
		t.Fatal(err)
	}
	if err := h.store.SetCacheBytes(ctx, heldHash, store.WholeTorrent, 700<<20, true); err != nil {
		t.Fatal(err)
	}
	return h, eng
}

func (h *harness) call(t *testing.T, method, target, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:12345"
	req.Host = "127.0.0.1:4321"
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)

	var out map[string]any
	json.NewDecoder(rec.Body).Decode(&out)
	return rec.Code, out
}

func queuedKeys(t *testing.T, h *harness) (keys []string, paused bool) {
	t.Helper()
	code, body := h.call(t, "GET", "/api/download/queue", "")
	if code != http.StatusOK {
		t.Fatalf("queue answered %d: %v", code, body)
	}
	for _, item := range body["items"].([]any) {
		keys = append(keys, item.(map[string]any)["epKey"].(string))
	}
	return keys, body["paused"].(bool)
}

func TestQueueListsWhatIsWaitingInOrder(t *testing.T) {
	h, _ := downloadsHarness(t)

	code, body := h.call(t, "GET", "/api/download/queue", "")
	if code != http.StatusOK {
		t.Fatalf("status %d: %v", code, body)
	}
	if keys, _ := queuedKeys(t, h); strings.Join(keys, ",") != "2,3,4" {
		t.Errorf("queue lists %v, want 2,3,4", keys)
	}
	if waiting := body["waiting"].(map[string]any); waiting["1"] != float64(3) {
		t.Errorf("waiting = %v, want three for the show", waiting)
	}
}

func TestPauseAllAndResumeOverTheAPI(t *testing.T) {
	h, _ := downloadsHarness(t)

	if code, body := h.call(t, "POST", "/api/download/queue/pause", ""); code != http.StatusOK || body["paused"] != true {
		t.Fatalf("pause answered %d %v", code, body)
	}
	if _, paused := queuedKeys(t, h); !paused {
		t.Error("the queue does not report itself paused")
	}

	if code, body := h.call(t, "POST", "/api/download/queue/resume", ""); code != http.StatusOK || body["paused"] != false {
		t.Fatalf("resume answered %d %v", code, body)
	}
	if _, paused := queuedKeys(t, h); paused {
		t.Error("the queue still reports itself paused after resume")
	}
}

// "Download now" puts the episode first, and asking for a download un-pauses the queue.
func TestDownloadNowMovesAnEpisodeFirstAndResumes(t *testing.T) {
	h, _ := downloadsHarness(t)
	h.call(t, "POST", "/api/download/queue/pause", "")

	code, body := h.call(t, "POST", "/api/download/queue/now", `{"animeId":1,"epKey":"4"}`)
	if code != http.StatusOK || body["moved"] != true {
		t.Fatalf("answered %d %v", code, body)
	}
	keys, paused := queuedKeys(t, h)
	if strings.Join(keys, ",") != "4,2,3" {
		t.Errorf("queue is %v, want 4 first", keys)
	}
	if paused {
		t.Error("the queue stayed paused after Download now")
	}

	if _, body := h.call(t, "POST", "/api/download/queue/now", `{"animeId":1,"epKey":"99"}`); body["moved"] != false {
		t.Errorf("an episode not in the queue was reported moved: %v", body)
	}
	if code, _ := h.call(t, "POST", "/api/download/queue/now", `not json`); code != http.StatusBadRequest {
		t.Errorf("a malformed request answered %d, want 400", code)
	}
}

func TestCancelDropsOneEpisodeOrTheWholeShow(t *testing.T) {
	h, _ := downloadsHarness(t)

	if code, body := h.call(t, "DELETE", "/api/download/all/1?episode=3", ""); code != http.StatusOK || body["cancelled"] != float64(1) {
		t.Fatalf("cancelling one answered %d %v", code, body)
	}
	if keys, _ := queuedKeys(t, h); strings.Join(keys, ",") != "2,4" {
		t.Errorf("queue is %v, want 2,4", keys)
	}

	if _, body := h.call(t, "DELETE", "/api/download/all/1", ""); body["cancelled"] != float64(2) {
		t.Errorf("cancelling the show removed %v, want 2", body["cancelled"])
	}
	if keys, _ := queuedKeys(t, h); len(keys) != 0 {
		t.Errorf("queue still holds %v", keys)
	}
	if code, _ := h.call(t, "DELETE", "/api/download/all/zero", ""); code != http.StatusBadRequest {
		t.Errorf("a missing show id answered %d, want 400", code)
	}
}

func TestPausingOneDownloadReachesTheEngine(t *testing.T) {
	h, eng := downloadsHarness(t)

	if code, body := h.call(t, "POST", "/api/downloads/"+heldHash+"/pause", ""); code != http.StatusOK || body["paused"] != true {
		t.Fatalf("pause answered %d %v", code, body)
	}
	if !eng.did("pause") {
		t.Error("the engine was never told to pause")
	}
	if code, body := h.call(t, "POST", "/api/downloads/"+heldHash+"/resume", ""); code != http.StatusOK || body["paused"] != false {
		t.Fatalf("resume answered %d %v", code, body)
	}
	if !eng.did("start") {
		t.Error("the engine was never told to start again")
	}
	if code, _ := h.call(t, "POST", "/api/downloads/"+strings.Repeat("b", 40)+"/pause", ""); code == http.StatusOK {
		t.Error("pausing a download the engine does not hold was reported as done")
	}
}

func kept(t *testing.T, h *harness) bool {
	t.Helper()
	entries, err := h.store.CacheEntries(context.Background())
	if err != nil || len(entries) != 1 {
		t.Fatalf("cache entries: %v, %v", entries, err)
	}
	return entries[0].Kept
}

func TestKeepAndUnkeepADownload(t *testing.T) {
	h, _ := downloadsHarness(t)

	code, body := h.call(t, "POST", "/api/downloads/"+heldHash+"/keep", "")
	if code != http.StatusOK || body["kept"] != true || body["infoHash"] != heldHash {
		t.Fatalf("keep answered %d %v", code, body)
	}
	if !kept(t, h) {
		t.Error("the download was not marked kept")
	}
	if code, _ := h.call(t, "POST", "/api/downloads/"+heldHash+"/unkeep", ""); code != http.StatusOK || kept(t, h) {
		t.Errorf("unkeep answered %d and left it kept=%v", code, kept(t, h))
	}
	if code, _ := h.call(t, "POST", "/api/downloads/"+strings.Repeat("b", 40)+"/keep", ""); code == http.StatusOK {
		t.Error("keeping a download that does not exist was reported as done")
	}
}

// What is playing is never pulled out from under the viewer.
func TestRemoveRefusesWhatIsPlayingThenDeletes(t *testing.T) {
	h, eng := downloadsHarness(t)
	ctx := context.Background()

	if err := h.store.PinCache(ctx, heldHash, store.WholeTorrent, true); err != nil {
		t.Fatal(err)
	}
	if code, _ := h.call(t, "DELETE", "/api/downloads/"+heldHash, ""); code == http.StatusOK || eng.did("delete") {
		t.Fatalf("a playing episode was removed (status %d)", code)
	}

	if err := h.store.PinCache(ctx, heldHash, store.WholeTorrent, false); err != nil {
		t.Fatal(err)
	}
	code, body := h.call(t, "DELETE", "/api/downloads/"+heldHash, "")
	if code != http.StatusOK || body["freedBytes"] != float64(700<<20) {
		t.Fatalf("remove answered %d %v", code, body)
	}
	if !eng.did("delete") {
		t.Error("the engine was never told to delete the data")
	}
	if entries, _ := h.store.CacheEntries(ctx); len(entries) != 0 {
		t.Errorf("the record of the download is still there: %v", entries)
	}
}

func TestClearSparesKeptDownloads(t *testing.T) {
	h, _ := downloadsHarness(t)
	h.call(t, "POST", "/api/downloads/"+heldHash+"/keep", "")

	if _, body := h.call(t, "POST", "/api/downloads/clear", ""); body["removed"] != float64(0) {
		t.Fatalf("clear removed a kept download: %v", body)
	}
	h.call(t, "POST", "/api/downloads/"+heldHash+"/unkeep", "")
	if _, body := h.call(t, "POST", "/api/downloads/clear", ""); body["removed"] != float64(1) {
		t.Errorf("clear removed %v, want the one download", body["removed"])
	}
}

// Without an engine the page gets a clear refusal, not a crash.
func TestDownloadActionsWithoutAnEngine(t *testing.T) {
	h := newHarness(t, config.Config{}, nil)

	if code, body := h.call(t, "GET", "/api/downloads", ""); code != http.StatusOK || len(body["items"].([]any)) != 0 {
		t.Errorf("the list answered %d %v, want an empty list", code, body)
	}
	for _, target := range []string{"POST /api/downloads/x/pause", "POST /api/downloads/x/keep", "DELETE /api/downloads/x", "POST /api/downloads/clear"} {
		method, path, _ := strings.Cut(target, " ")
		if code, _ := h.call(t, method, path, ""); code != http.StatusServiceUnavailable {
			t.Errorf("%s answered %d, want 503", target, code)
		}
	}
}
