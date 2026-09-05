package library

import (
	"context"
	"testing"
)

// Evicting deletes the whole torrent, so a season pack with one episode playing
// must be left alone entirely: clearing the idle files of that pack would take
// the running one with them.
func TestClearKeepsEveryFileOfAPinnedTorrent(t *testing.T) {
	c, st := newCache(t)
	ctx := context.Background()

	// One pack, two files: episode 3 is playing, episode 7 is idle.
	cache(t, st, "pack", 3, 1<<30, true)
	cache(t, st, "pack", 7, 1<<30, false)

	removed, freed, err := c.Clear(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 0 || freed != 0 {
		t.Fatalf("cleared %d entries (%d bytes) of a torrent that is playing", removed, freed)
	}

	// Both files are still known, so nothing was dropped from under the player.
	entries, err := st.CacheEntries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("%d cache entries left, want both files of the pack", len(entries))
	}
}

// A torrent nobody is playing is still cleared, or the button does nothing.
func TestClearRemovesIdleTorrents(t *testing.T) {
	c, st := newCache(t)
	ctx := context.Background()

	cache(t, st, "idle", 1, 1<<30, false)
	cache(t, st, "idle", 2, 1<<30, false)

	removed, _, err := c.Clear(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed %d, want the one torrent counted once", removed)
	}
}
