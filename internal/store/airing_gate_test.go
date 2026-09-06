package store

import "testing"

// The catalogue names only the next episode's air time. Once that passes and
// before the row is refreshed, the one after it must still count as unaired,
// or a stray "07" gets announced days early.
func TestAiredKnowsOnlyTheNextEpisode(t *testing.T) {
	const next, at = 7, int64(1_000_000)
	for _, tt := range []struct {
		episode int
		now     int64
		want    bool
	}{
		{6, at - 1, true},  // already broadcast
		{7, at - 1, false}, // not yet
		{7, at, true},      // just now
		{8, at + 3600, false},
		{8, at + 7*86400, false}, // however stale the row
	} {
		if got := aired(tt.episode, next, at, tt.now); got != tt.want {
			t.Errorf("aired(%d) at %+d = %v, want %v", tt.episode, tt.now-at, got, tt.want)
		}
	}
	if !aired(3, 0, 0, at) {
		t.Error("with no schedule at all, nothing can be held back")
	}
}
