package torrent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSession(t *testing.T, dir string, torrents map[string]map[string]any) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"torrents": torrents, "version": 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "session.json"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tr := range torrents {
		name := tr["info_hash"].(string) + ".torrent"
		if err := os.WriteFile(filepath.Join(dir, name), []byte("d4:infoe"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// Moving to a private session keeps this install's downloads and leaves every
// other install's torrents behind.
func TestSeedSessionCarriesOverOnlyOurTorrents(t *testing.T) {
	legacy, dst, cache := t.TempDir(), filepath.Join(t.TempDir(), "session"), t.TempDir()
	other := t.TempDir()
	writeSession(t, legacy, map[string]map[string]any{
		"0": {"info_hash": "aaaa", "output_folder": cache + string(filepath.Separator)},
		"1": {"info_hash": "bbbb", "output_folder": filepath.Join(cache, "Show S01")},
		"2": {"info_hash": "cccc", "output_folder": other},
	})

	n, err := SeedSession(dst, legacy, cache)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("carried %d torrents, want 2", n)
	}

	raw, err := os.ReadFile(filepath.Join(dst, "session.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Torrents map[string]struct {
			InfoHash string `json:"info_hash"`
		} `json:"torrents"`
		Version int `json:"version"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Torrents) != 2 || doc.Torrents["2"].InfoHash != "" {
		t.Errorf("session = %+v, want only aaaa and bbbb", doc.Torrents)
	}
	if doc.Version != 1 {
		t.Error("other session fields were dropped")
	}
	for hash, want := range map[string]bool{"aaaa": true, "bbbb": true, "cccc": false} {
		_, err := os.Stat(filepath.Join(dst, hash+".torrent"))
		if (err == nil) != want {
			t.Errorf("%s.torrent copied = %v, want %v", hash, err == nil, want)
		}
	}
}

// Once seeded the private session is the engine's own; a second seed must not
// overwrite what it has written since.
func TestSeedSessionRunsOnce(t *testing.T) {
	legacy, dst, cache := t.TempDir(), t.TempDir(), t.TempDir()
	writeSession(t, legacy, map[string]map[string]any{"0": {"info_hash": "aaaa", "output_folder": cache}})
	if err := os.WriteFile(filepath.Join(dst, "session.json"), []byte(`{"torrents":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if n, err := SeedSession(dst, legacy, cache); err != nil || n != 0 {
		t.Fatalf("seeded %d (%v) over an existing session", n, err)
	}
	if raw, _ := os.ReadFile(filepath.Join(dst, "session.json")); string(raw) != `{"torrents":{}}` {
		t.Errorf("existing session overwritten: %s", raw)
	}
}

func TestSeedSessionWithNoLegacySession(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "session")
	if n, err := SeedSession(dst, t.TempDir(), t.TempDir()); err != nil || n != 0 {
		t.Fatalf("seeded %d (%v) from nothing", n, err)
	}
	if _, err := os.Stat(dst); err != nil {
		t.Error("the session folder should exist for rqbit to write into")
	}
}

func TestWithin(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cache")
	cases := map[string]bool{
		dir:                                    true,
		dir + string(filepath.Separator):       true,
		filepath.Join(dir, "Show"):             true,
		filepath.Join(dir, "..", "cache2"):     false,
		dir + "2":                              false,
		filepath.Join(dir, "..", "..", "else"): false,
		"":                                     false,
	}
	for path, want := range cases {
		if got := Within(dir, path); got != want {
			t.Errorf("Within(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestRqbitArgsGiveTheEngineItsOwnSession(t *testing.T) {
	s := testSupervisor(Options{SessionDir: "/data/rqbit"})
	args := s.rqbitArgs("/cache")
	if valueAfter(args, "--persistence-location") != "/data/rqbit" {
		t.Fatalf("no private session: %v", args)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "server start --persistence-location /data/rqbit /cache") {
		t.Errorf("the session flag belongs to server start, before the cache dir: %v", args)
	}
}

// Headers arrive before the first byte, so a stream that sends them and then
// nothing (or an error status) is not a release delivering.
func TestPrewarmFailsWhenNothingArrives(t *testing.T) {
	short := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "2097152")
		w.WriteHeader(http.StatusPartialContent)
		w.Write(make([]byte, 1000))
	}))
	defer short.Close()
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "invalid state", http.StatusInternalServerError)
	}))
	defer failing.Close()
	small := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		w.WriteHeader(http.StatusPartialContent)
		w.Write(make([]byte, 1000))
	}))
	defer small.Close()

	c := NewClient("http://127.0.0.1:1")
	ctx := t.Context()
	if err := c.readRange(ctx, short.URL, "bytes=0-2097151", 2<<20); err == nil {
		t.Error("a short read counted as delivery")
	}
	if err := c.readRange(ctx, failing.URL, "bytes=0-2097151", 2<<20); err == nil {
		t.Error("an error status counted as delivery")
	}
	if err := c.readRange(ctx, small.URL, "bytes=0-2097151", 2<<20); err != nil {
		t.Errorf("a file smaller than the window is delivered whole: %v", err)
	}
}

func TestFileDoneIsPerFile(t *testing.T) {
	d := Detail{Files: []File{{Length: 100}, {Length: 200}}}
	s := Stats{Finished: false, FileProgress: []int64{100, 50}}
	if !s.FileDone(d, 0) || s.FileDone(d, 1) {
		t.Errorf("FileDone = %v %v, want true false", s.FileDone(d, 0), s.FileDone(d, 1))
	}
	if (Stats{Finished: true}).FileDone(d, 1) != true {
		t.Error("without per-file progress the torrent's own state decides")
	}
}
