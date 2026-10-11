package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"kuro/internal/config"
	"kuro/internal/library"
	"kuro/internal/store"
)

// localHarness is a server with a scanner and two library files on record: one
// matched to show 1, one unmatched. It returns their ids and the folder.
func localHarness(t *testing.T) (h *harness, matched, unmatched int, dir string) {
	t.Helper()
	dir = t.TempDir()
	h = newHarness(t, config.Config{}, nil)
	h.server.scanner = library.NewScanner(h.store, slog.New(slog.NewTextHandler(io.Discard, nil)))

	ctx := context.Background()
	if err := h.store.EnsureAnime(ctx, 1); err != nil {
		t.Fatal(err)
	}
	episode := filepath.Join(dir, "Show - 01.mkv")
	if err := os.WriteFile(episode, []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.SaveLocalFiles(ctx, 1, []store.LocalFile{
		{Path: episode, Size: 10, AnimeID: 1, Episode: 1, Season: 1, Confidence: 1},
		{Path: filepath.Join(dir, "mystery.mkv"), Size: 10},
	}); err != nil {
		t.Fatal(err)
	}
	files, err := h.store.LocalFiles(ctx, 0, false, store.Paging{Page: 1, PerPage: 10})
	if err != nil || len(files.Items) != 2 {
		t.Fatalf("library files: %+v, %v", files.Items, err)
	}
	for _, f := range files.Items {
		if f.AnimeID == 1 {
			matched = f.ID
		} else {
			unmatched = f.ID
		}
	}
	return h, matched, unmatched, dir
}

func TestLocalFilesListsByShowAndUnmatched(t *testing.T) {
	h, _, _, _ := localHarness(t)
	for target, want := range map[string]int{
		"/api/local/files":                2,
		"/api/local/files?anime=1":        1,
		"/api/local/files?unmatched=true": 1,
		"/api/local/files?anime=2":        0,
	} {
		code, body := h.call(t, "GET", target, "")
		if code != http.StatusOK || len(body["items"].([]any)) != want {
			t.Errorf("%s answered %d with %d files, want %d", target, code, len(body["items"].([]any)), want)
		}
	}
}

// Seeking in the browser is a range request against the file.
func TestLocalStreamServesRangesAndOnlyRecordedFiles(t *testing.T) {
	h, matched, unmatched, _ := localHarness(t)

	req := httptest.NewRequest("GET", fmt.Sprintf("/api/local/%d/stream", matched), nil)
	req.RemoteAddr, req.Host = "127.0.0.1:12345", "127.0.0.1:4321"
	req.Header.Set("Range", "bytes=2-5")
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusPartialContent || rec.Body.String() != "2345" {
		t.Fatalf("a range answered %d %q, want 206 with bytes 2-5", rec.Code, rec.Body.String())
	}

	if code, _, _ := h.fetch(t, "GET", "/api/local/9999/stream"); code != http.StatusNotFound {
		t.Errorf("a file never recorded answered %d, want 404", code)
	}
	// Recorded, but no longer on disk.
	if code, _, _ := h.fetch(t, "GET", fmt.Sprintf("/api/local/%d/stream", unmatched)); code != http.StatusNotFound {
		t.Errorf("a file gone from disk answered %d, want 404", code)
	}
	if code, _, _ := h.fetch(t, "GET", "/api/local/0/stream"); code != http.StatusBadRequest {
		t.Errorf("id 0 answered %d, want 400", code)
	}
}

// A typo in a folder is reported when it is set, not as an empty scan later.
func TestLibraryPathsAreCheckedBeforeTheyAreSaved(t *testing.T) {
	h, _, _, dir := localHarness(t)
	ctx := context.Background()
	paths := func(list ...string) string {
		b, _ := json.Marshal(map[string]any{"paths": list})
		return string(b)
	}

	// Nothing configured yet, so there is nothing to scan.
	if code, _ := h.call(t, "POST", "/api/local/scan", ""); code != http.StatusPreconditionFailed {
		t.Errorf("a scan with no folders answered %d, want 412", code)
	}

	missing := filepath.Join(dir, "nope")
	code, body := h.call(t, "POST", "/api/local/paths", paths(dir, missing, filepath.Join(dir, "Show - 01.mkv")))
	if code != http.StatusBadRequest || len(body["paths"].(map[string]any)) != 2 {
		t.Fatalf("bad folders answered %d %v, want both named", code, body)
	}
	if roots, _ := h.server.scanner.Roots(ctx); len(roots) != 0 {
		t.Fatalf("folders were saved despite the refusal: %v", roots)
	}

	if code, body := h.call(t, "POST", "/api/local/paths", paths(dir, "")); code != http.StatusOK || len(body["paths"].([]any)) != 1 {
		t.Fatalf("a good folder answered %d %v", code, body)
	}
	if roots, _ := h.server.scanner.Roots(ctx); len(roots) != 1 {
		t.Errorf("roots are %v, want the one folder", roots)
	}
	if code, body := h.call(t, "GET", "/api/local", ""); code != http.StatusOK || len(body["roots"].([]any)) != 1 {
		t.Errorf("the library summary answered %d %v", code, body)
	}
}

// Folders are the host's to change: a phone on the network cannot repoint the library.
func TestLibraryPathsCanOnlyBeSetFromTheHost(t *testing.T) {
	h, _, _, dir := localHarness(t)
	body, _ := json.Marshal(map[string]any{"paths": []string{dir}})

	req := httptest.NewRequest("POST", "/api/local/paths", bytes.NewReader(body))
	req.RemoteAddr, req.Host = "192.168.1.50:40000", "192.168.1.10:4321"
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Fatal("another device changed the library folders")
	}
	if roots, _ := h.server.scanner.Roots(context.Background()); len(roots) != 0 {
		t.Errorf("folders were saved from another device: %v", roots)
	}
}

func TestForgetMissingDropsOnlyFilesFlaggedGone(t *testing.T) {
	h, _, _, _ := localHarness(t)
	if code, body := h.call(t, "POST", "/api/local/forget", ""); code != http.StatusOK || body["forgotten"] != float64(0) {
		t.Errorf("answered %d %v, want nothing forgotten: no scan has flagged a file", code, body)
	}
	if _, body := h.call(t, "GET", "/api/local/files", ""); len(body["items"].([]any)) != 2 {
		t.Errorf("files were dropped: %v", body["items"])
	}
}
