package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func inTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("KURO_ROOT", dir)
	t.Setenv("LOCALAPPDATA", filepath.Join(dir, "appdata"))
	return dir
}

func TestLoadWritesTemplateOnFirstRun(t *testing.T) {
	dir := inTempDir(t)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}

	written, err := os.ReadFile(filepath.Join(dir, "config.toml"))
	if err != nil {
		t.Fatalf("template not written: %v", err)
	}
	if len(written) == 0 {
		t.Fatal("template is empty")
	}

	if cfg.Addr != "127.0.0.1:4321" {
		t.Errorf("addr = %q", cfg.Addr)
	}
	if cfg.AniList.ClientID != "" {
		t.Errorf("template should not ship credentials, got %q", cfg.AniList.ClientID)
	}
}

func TestFolderSettingsResolveAgainstTheExe(t *testing.T) {
	dir := inTempDir(t)
	abs := filepath.Join(dir, "elsewhere", "tools")
	toml := "cache_dir = \"store\"\nbin_dir = '" + abs + "'\ndata_dir = \"data\"\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for got, want := range map[string]string{
		cfg.CacheDir:  filepath.Join(dir, "store"),
		cfg.BinDir:    abs,
		cfg.DataDir(): filepath.Join(dir, "data"),
	} {
		if got != want {
			t.Errorf("got %s, want %s", got, want)
		}
	}
}

func TestLoadCreatesDataAndCacheDirs(t *testing.T) {
	inTempDir(t)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}

	for _, dir := range []string{cfg.DataDir(), cfg.CacheDir} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Errorf("%s not created: %v", dir, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("%s is not a directory", dir)
		}
	}
}

func TestLoadReadsExistingFile(t *testing.T) {
	dir := inTempDir(t)

	const contents = `
addr = "0.0.0.0:9000"

[anilist]
client_id = "abc"
client_secret = "shh"
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != "0.0.0.0:9000" {
		t.Errorf("addr = %q", cfg.Addr)
	}
	if cfg.AniList.ClientID != "abc" || cfg.AniList.ClientSecret != "shh" {
		t.Errorf("credentials = %+v", cfg.AniList)
	}
	if cfg.RedirectURI() != "http://localhost:9000/callback" {
		t.Errorf("redirect = %q, should follow the configured port", cfg.RedirectURI())
	}
}

// A root under the temp directory is what an exe opened from inside a zip gets,
// and the page has to be able to say so.
func TestLoadKnowsATemporaryRoot(t *testing.T) {
	dir := inTempDir(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Temporary() {
		t.Errorf("%s is under %s and should count as temporary", dir, os.TempDir())
	}
	if cfg.ConfigPath() != filepath.Join(dir, "config.toml") {
		t.Errorf("config path = %q", cfg.ConfigPath())
	}
	if cfg.StrayConfig() != "" {
		t.Errorf("no stray file, got %q", cfg.StrayConfig())
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if cfg.StrayConfig() != filepath.Join(dir, "config.toml.txt") {
		t.Errorf("stray = %q", cfg.StrayConfig())
	}
}

// data_dir moves the database out of AppData; written by SetDataDir above any
// table, so it stays a top-level key.
func TestDataDirIsChosenAndWrittenBack(t *testing.T) {
	dir := inTempDir(t)
	cfg, err := Load()
	if err != nil || cfg.DataDir() != filepath.Join(dir, "appdata", "kuro") {
		t.Fatalf("default data dir = %q (%v)", cfg.DataDir(), err)
	}

	const withTable = "addr = \"127.0.0.1:4321\"\n\n[[indexer]]\ntype = \"nyaa\"\nurl = \"https://a.example\"\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(withTable), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetDataDir("data"); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DataDir() != filepath.Join(dir, "data") || cfg.DatabasePath() != filepath.Join(dir, "data", "kuro.db") {
		t.Errorf("data dir = %q", cfg.DataDir())
	}
	if len(cfg.Indexers) != 1 {
		t.Errorf("the indexer table was disturbed: %+v", cfg.Indexers)
	}

	// Set again: the line is replaced, not duplicated; empty is the default.
	if err := cfg.SetDataDir(""); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "config.toml"))
	if strings.Count(string(raw), "data_dir") != 1 {
		t.Errorf("data_dir written more than once:\n%s", raw)
	}
	if cfg, _ = Load(); cfg.DataDir() != filepath.Join(dir, "appdata", "kuro") {
		t.Errorf("empty should mean the default, got %q", cfg.DataDir())
	}
}

// Sites are the user's to name: a fresh config has none, a written one keeps
// its order.
func TestLoadReadsIndexersInOrder(t *testing.T) {
	dir := inTempDir(t)
	if cfg, err := Load(); err != nil || len(cfg.Indexers) != 0 {
		t.Fatalf("fresh config: indexers=%v err=%v", cfg.Indexers, err)
	}

	const contents = `
[[indexer]]
type = "tokyotosho"
url = "https://b.example"

[[indexer]]
type = "nyaa"
url = "https://a.example"
adult = true
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	want := []Indexer{
		{Type: "tokyotosho", URL: "https://b.example"},
		{Type: "nyaa", URL: "https://a.example", Adult: true},
	}
	if len(cfg.Indexers) != 2 || cfg.Indexers[0] != want[0] || cfg.Indexers[1] != want[1] {
		t.Errorf("indexers = %+v, want %+v", cfg.Indexers, want)
	}
}

func TestLoadDoesNotOverwriteExistingFile(t *testing.T) {
	dir := inTempDir(t)
	path := filepath.Join(dir, "config.toml")

	const contents = "addr = \"127.0.0.1:5555\"\n"
	os.WriteFile(path, []byte(contents), 0o600)

	if _, err := Load(); err != nil {
		t.Fatal(err)
	}

	after, _ := os.ReadFile(path)
	if string(after) != contents {
		t.Fatalf("config.toml was rewritten:\n%s", after)
	}
}

func TestLoadRejectsMalformedToml(t *testing.T) {
	dir := inTempDir(t)
	os.WriteFile(filepath.Join(dir, "config.toml"), []byte("addr = = ="), 0o600)

	if _, err := Load(); err == nil {
		t.Fatal("expected a parse error rather than silent defaults")
	}
}

func TestDatabaseLivesUnderLocalAppData(t *testing.T) {
	dir := inTempDir(t)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}

	want := filepath.Join(dir, "appdata", "kuro", "kuro.db")
	if cfg.DatabasePath() != want {
		t.Fatalf("database at %q, want %q", cfg.DatabasePath(), want)
	}
}

func TestPathResolvesAgainstBinDir(t *testing.T) {
	inTempDir(t)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Path("mpv.exe"); got != filepath.Join(cfg.BinDir, "mpv.exe") {
		t.Fatalf("got %q", got)
	}
}
