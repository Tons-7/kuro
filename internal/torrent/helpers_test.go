package torrent

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

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
