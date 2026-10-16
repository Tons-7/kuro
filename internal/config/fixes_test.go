package config

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A cache folder holding kuro's own folders would hand them to the orphan cleanup; the config refuses it.
func TestCacheDirMustNotHoldKurosOwnFolders(t *testing.T) {
	for _, body := range []string{
		"cache_dir = \".\"\n",
		"cache_dir = \"store\"\nbin_dir = \"store/bin\"\n",
		"cache_dir = \"store\"\ndata_dir = \"store/data\"\n",
	} {
		dir := inTempDir(t)
		if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "cache_dir") {
			t.Errorf("%q: err = %v, want the cache folder refused", body, err)
		}
	}
	dir := inTempDir(t)
	os.WriteFile(filepath.Join(dir, "config.toml"), []byte("cache_dir = \"store\"\nbin_dir = \"tools\"\n"), 0o600)
	if _, err := Load(); err != nil {
		t.Fatalf("separate folders refused: %v", err)
	}
}

// A broken config.toml names the file, so the message in the log says where to look.
func TestConfigParseErrorNamesTheFile(t *testing.T) {
	dir := inTempDir(t)
	os.WriteFile(filepath.Join(dir, "config.toml"), []byte("cache_dir = \n"), 0o600)
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "config.toml") {
		t.Fatalf("err = %v", err)
	}
}

// A second launch hands its URL to the running window: the browser exits clean at once, and that is
// the window, not a reason to retry and open a tab.
func TestSecondLaunchTakesACleanExitAsTheHandOff(t *testing.T) {
	var attempts int
	oldWindow, oldTab := showWindow, showTab
	tabs := 0
	showWindow = func(_ context.Context, _, profile string, _, handoff bool) *launched {
		attempts++
		if handoff && profile == "P" {
			return &launched{}
		}
		return nil
	}
	showTab = func(string) { tabs++ }
	t.Cleanup(func() { showWindow, showTab = oldWindow, oldTab })

	OpenApp(context.Background(), "http://localhost:4321", "P")
	if attempts != 1 || tabs != 0 {
		t.Fatalf("attempts %d, tabs %d; want one hand-off and no tab", attempts, tabs)
	}
}
