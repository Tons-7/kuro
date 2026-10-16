package library

import (
	"testing"
	"time"

	"kuro/internal/metadata"
)

// The caches hold at most cacheShows shows, the oldest going first, and drop what has expired.
func TestPruneBoundsTheEnricherCaches(t *testing.T) {
	at := func(c castEntry) time.Time { return c.at }
	m := map[int]castEntry{}
	now := time.Now()
	for id := range cacheShows {
		m[id] = castEntry{at: now.Add(time.Duration(id) * time.Second)}
	}
	m[-1] = castEntry{at: now.Add(-castTTL - time.Minute)}

	prune(m, at, castTTL)

	if _, ok := m[-1]; ok {
		t.Error("an expired entry was kept")
	}
	if _, ok := m[0]; ok {
		t.Error("the oldest entry should make room for the next")
	}
	if len(m) != cacheShows-1 {
		t.Fatalf("len = %d, want room for exactly one more (%d)", len(m), cacheShows-1)
	}
}

// An answer missing its staff is asked for again soon; a whole one is kept for the cast's TTL.
func TestPartialExtraIsNotKeptForLong(t *testing.T) {
	stale := time.Now().Add(-partialTTL - time.Second)
	if (extraEntry{extra: metadata.Extra{StaffMissing: true}, at: stale}).fresh() {
		t.Error("a partial answer outlived partialTTL")
	}
	if !(extraEntry{extra: metadata.Extra{}, at: stale}).fresh() {
		t.Error("a whole answer should be kept for castTTL")
	}
}
