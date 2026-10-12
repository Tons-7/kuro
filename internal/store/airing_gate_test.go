package store

import (
	"testing"
	"time"
)

// Only the next episode's air time is known: once it passes, before the row is refreshed, the one
// after must still count as unaired, or a stray "07" gets announced days early.
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

// The one episode that is news: the last to air, or the finale of a show that ended this week.
func TestLatestIsTheEpisodeThatAiredLast(t *testing.T) {
	now := time.Now()
	day := func(d int) string { return now.AddDate(0, 0, d).Format("2006-01-02") }
	for _, tt := range []struct {
		name string
		f    Follow
		want int
	}{
		{"next airs tomorrow", Follow{NextEpisode: 6, NextAiringAt: now.Unix() + 3600}, 5},
		{"next's time has passed", Follow{NextEpisode: 6, NextAiringAt: now.Unix() - 60}, 6},
		{"next known, time unknown", Follow{NextEpisode: 6}, 5},
		{"first episode still to air", Follow{NextEpisode: 1, NextAiringAt: now.Unix() + 3600}, 0},
		{"ended yesterday", Follow{Episodes: 12, EndDate: day(-1)}, 12},
		{"ended today", Follow{Episodes: 12, EndDate: day(0)}, 12},
		{"ended last month", Follow{Episodes: 12, EndDate: day(-30)}, 0},
		{"no airing data", Follow{Episodes: 12}, 0},
		{"end date unreadable", Follow{Episodes: 12, EndDate: "2024"}, 0},
	} {
		if got := tt.f.Latest(now.Unix()); got != tt.want {
			t.Errorf("%s: Latest = %d, want %d", tt.name, got, tt.want)
		}
	}
}
