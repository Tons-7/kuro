package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kuro/internal/config"
)

// Choosing a data folder copies the database there and records the folder in
// config.toml; a folder that already holds one is left alone.
func TestSetDataDirCopiesTheDatabaseAndWritesConfig(t *testing.T) {
	root := t.TempDir()
	t.Setenv("KURO_ROOT", root)
	t.Setenv("LOCALAPPDATA", filepath.Join(root, "appdata"))
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.DatabasePath(), []byte("history"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, cfg, nil)

	post := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/setup/data-dir", strings.NewReader(`{"path":"`+path+`"}`))
		req.RemoteAddr = "127.0.0.1:1"
		req.Host = "127.0.0.1:4321"
		rec := httptest.NewRecorder()
		h.handler.ServeHTTP(rec, req)
		return rec
	}

	if rec := post("data"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"copied":true`) {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if got, _ := os.ReadFile(filepath.Join(root, "data", "kuro.db")); string(got) != "history" {
		t.Errorf("database not copied: %q", got)
	}
	if again, _ := config.Load(); again.DataDir() != filepath.Join(root, "data") {
		t.Errorf("config.toml not updated: %q", again.DataDir())
	}

	// A second folder with its own database keeps it.
	other := filepath.Join(root, "other")
	os.Mkdir(other, 0o755)
	os.WriteFile(filepath.Join(other, "kuro.db"), []byte("theirs"), 0o600)
	if rec := post("other"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"copied":false`) {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if got, _ := os.ReadFile(filepath.Join(other, "kuro.db")); string(got) != "theirs" {
		t.Errorf("an existing database was overwritten: %q", got)
	}
}
