package server

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"kuro/internal/config"
	"kuro/internal/store"
)

// The on-disk copy is found for a single file and for a pack, whose episodes
// sit in a folder named after it, even with another release's file of the same
// name in the cache root.
func TestReadablePathFindsPackEpisodes(t *testing.T) {
	cache := t.TempDir()
	h := newHarness(t, config.Config{CacheDir: cache}, nil)
	ctx := context.Background()
	engineURL := "http://127.0.0.1:1/torrents/1/stream/1"
	os.WriteFile(filepath.Join(cache, "Show - 03.mkv"), []byte("another release"), 0o644)

	for _, c := range []struct {
		name, file, onDisk string
		episode            int
	}{
		{"[Group] Show - 01.mkv", "[Group] Show - 01.mkv", filepath.Join(cache, "[Group] Show - 01.mkv"), 1},
		{"Show Pack", "Show - 02.mkv", filepath.Join(cache, "Show Pack", "Show - 02.mkv"), 2},
		{"Other Pack", "Show - 03.mkv", filepath.Join(cache, "Other Pack", "Show - 03.mkv"), 3},
	} {
		if err := os.MkdirAll(filepath.Dir(c.onDisk), 0o755); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(c.onDisk, []byte("data"), 0o644)
		if err := h.store.RecordTorrent(ctx, store.TorrentRecord{
			InfoHash: c.name, Name: c.name, AnimeID: 7, EpKey: strconv.Itoa(c.episode), FilePath: c.file,
		}); err != nil {
			t.Fatal(err)
		}
		if got := h.server.readablePath(ctx, 7, c.episode, engineURL); got != c.onDisk {
			t.Errorf("%s: got %q, want %q", c.name, got, c.onDisk)
		}
	}
}
