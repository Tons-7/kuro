package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"kuro/internal/config"
	"kuro/internal/store"
)

func exportHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t, config.Config{}, nil)
	ctx := t.Context()

	episodes := 28
	if _, err := h.store.ImportList(ctx, []store.Anime{
		{ID: 100, Romaji: "Sousou no Frieren", Synonyms: "[]", Genres: "[]", Episodes: &episodes},
	}, nil, store.ImportMerge); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.MarkWatched(ctx, 100, 9); err != nil {
		t.Fatal(err)
	}
	return h
}

func postLibrary(t *testing.T, h *harness, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:12345"
	req.Host = "127.0.0.1:4321"
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)
	return rec
}

func getLibrary(t *testing.T, h *harness, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.RemoteAddr = "127.0.0.1:12345"
	req.Host = "127.0.0.1:4321"
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)
	return rec
}

func TestExportServesADownloadableFile(t *testing.T) {
	h := exportHarness(t)

	rec := getLibrary(t, h, "/api/library/export")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") ||
		!strings.Contains(cd, ".json") {
		t.Errorf("content-disposition = %q", cd)
	}

	var file LibraryFile
	if err := json.Unmarshal(rec.Body.Bytes(), &file); err != nil {
		t.Fatal(err)
	}
	if file.Kind != libraryKind || file.Version != libraryVersion || file.ExportedAt == "" {
		t.Fatalf("file = %+v", file)
	}
	if len(file.Entries) != 1 || file.Entries[0].AnimeID != 100 || file.Entries[0].Progress != 9 {
		t.Fatalf("entries = %+v", file.Entries)
	}
}

func TestLibraryCountsRoute(t *testing.T) {
	h := exportHarness(t)

	rec := getLibrary(t, h, "/api/library/counts")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var counts store.LibraryCounts
	if err := json.Unmarshal(rec.Body.Bytes(), &counts); err != nil {
		t.Fatal(err)
	}
	if counts.Total != 1 || counts.Statuses["CURRENT"] != 1 {
		t.Fatalf("counts = %+v", counts)
	}
}

func TestExportAsText(t *testing.T) {
	h := exportHarness(t)

	rec := getLibrary(t, h, "/api/library/export?format=txt")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("content-type = %q", ct)
	}
	line := strings.TrimSpace(rec.Body.String())
	if !strings.Contains(line, "Sousou no Frieren") || !strings.Contains(line, "9/28") {
		t.Errorf("line = %q, want the title and its progress", line)
	}
}

func TestImportAppliesAFile(t *testing.T) {
	h := exportHarness(t)

	rec := postLibrary(t, h, "/api/library/import", `{
	  "kind":"kuro.library","version":1,
	  "entries":[{"animeId":100,"progress":20,"status":"CURRENT","score":80}]
	}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}

	var rep store.ImportReport
	json.Unmarshal(rec.Body.Bytes(), &rep)
	if rep.Entries != 1 {
		t.Fatalf("report = %+v", rep)
	}
	if e, _ := h.store.ListEntry(t.Context(), 100); e.Progress != 20 || e.Score != 80 {
		t.Fatalf("entry = %+v", e)
	}
}

// A file with no kind is accepted (someone's hand-written list), but one that
// names a different tool, or a newer format, is not applied blindly.
func TestImportRejectsWhatItCannotRead(t *testing.T) {
	h := exportHarness(t)

	tests := map[string]string{
		"not json":       `nonsense`,
		"another tool":   `{"kind":"someone-elses.export","entries":[{"animeId":1}]}`,
		"newer version":  `{"kind":"kuro.library","version":99,"entries":[{"animeId":1}]}`,
		"nothing in it":  `{"kind":"kuro.library","version":1,"entries":[]}`,
		"entries absent": `{"kind":"kuro.library","version":1}`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			rec := postLibrary(t, h, "/api/library/import", body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
			}
		})
	}

	if e, _ := h.store.ListEntry(t.Context(), 100); e.Progress != 9 {
		t.Errorf("a rejected import changed the library: %+v", e)
	}
}

func TestKeepingOneEpisodeOfAPack(t *testing.T) {
	h := exportHarness(t)
	ctx := t.Context()
	const hash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	for i, ep := range []string{"1", "2"} {
		if err := h.store.RecordTorrent(ctx, store.TorrentRecord{
			InfoHash: hash, Name: "[Group] Sousou no Frieren (01-02) [Batch]", AnimeID: 100,
			EpKey: ep, FileIndex: i, FilePath: "ep" + ep + ".mkv", TotalSize: 200,
		}); err != nil {
			t.Fatal(err)
		}
	}

	rec := postLibrary(t, h, "/api/downloads/"+hash+"/files/1/keep", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}

	rec = getLibrary(t, h, "/api/downloads/"+hash+"/files")
	var body struct{ Items []store.DownloadFile }
	json.Unmarshal(rec.Body.Bytes(), &body)
	if len(body.Items) != 2 || body.Items[0].Kept || !body.Items[1].Kept {
		t.Fatalf("files = %+v, want only the second kept", body.Items)
	}

	if rec := postLibrary(t, h, "/api/downloads/"+hash+"/files/7/keep", ""); rec.Code != http.StatusNotFound {
		t.Errorf("status = %d for a file that is not there, want 404", rec.Code)
	}
	if rec := postLibrary(t, h, "/api/downloads/"+hash+"/files/x/keep", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d for a bad index, want 400", rec.Code)
	}
}

// The entries a bare list carries still apply, so a file written by hand works.
func TestImportAcceptsAFileWithoutAKind(t *testing.T) {
	h := exportHarness(t)

	rec := postLibrary(t, h, "/api/library/import", `{"entries":[{"animeId":100,"progress":11}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if e, _ := h.store.ListEntry(t.Context(), 100); e.Progress != 11 {
		t.Fatalf("entry = %+v", e)
	}
}
