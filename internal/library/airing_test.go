package library

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"kuro/internal/indexer"
	"kuro/internal/store"
)

// A show mid-cour: five episodes out, the sixth broadcasting tomorrow.
func airingStore(t *testing.T, nextEpisode int, airingAt int64, progress int) *store.Store {
	t.Helper()
	st := prefetchStore(t)
	ctx := context.Background()

	episodes := 13
	next := nextEpisode
	airing := int(airingAt)
	if _, err := st.ImportList(ctx, []store.Anime{{
		ID: 7, Romaji: "Bleach Sennen Kessen-hen Kashin Tan", Synonyms: "[]", Genres: "[]",
		Episodes: &episodes, NextEpisode: &next, NextAiringAt: &airing,
	}}, nil, store.ImportMerge); err != nil {
		t.Fatal(err)
	}
	if _, err := st.MarkWatched(ctx, 7, progress); err != nil {
		t.Fatal(err)
	}
	if err := st.SetFollow(ctx, store.Follow{AnimeID: 7}, true); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(ctx, "notify.enabled", "true"); err != nil {
		t.Fatal(err)
	}
	return st
}

func pollWith(t *testing.T, st *store.Store, results []indexer.Torrent) WatchReport {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	source := fixedIndexer{results: results}
	w := NewWatcher(st, NewFinder(st, source, log), source, log)

	rep, err := w.Poll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

// The reported bug: on Sunday kuro announced an episode airing the following
// Saturday, then failed to play what it had grabbed.
func TestNoNotificationBeforeTheEpisodeAirs(t *testing.T) {
	tomorrow := time.Now().Add(24 * time.Hour).Unix()
	st := airingStore(t, 6, tomorrow, 5)

	rep := pollWith(t, st, []indexer.Torrent{
		release("1111111111111111111111111111111111111111",
			"[Group] Bleach - Sennen Kessen-hen - Kashin Tan - 06 [1080p].mkv", 40),
	})

	if rep.Notified != 0 {
		t.Errorf("notified %d times about an episode that has not aired", rep.Notified)
	}
	items, _ := st.Notifications(context.Background(), false, 10)
	if len(items) != 0 {
		t.Errorf("notifications = %+v, want none", items)
	}
}

func TestNotifiesOnceTheEpisodeHasAired(t *testing.T) {
	anHourAgo := time.Now().Add(-time.Hour).Unix()
	st := airingStore(t, 6, anHourAgo, 5)

	rep := pollWith(t, st, []indexer.Torrent{
		release("1111111111111111111111111111111111111111",
			"[Group] Bleach - Sennen Kessen-hen - Kashin Tan - 06 [1080p].mkv", 40),
	})

	if rep.Notified != 1 {
		t.Fatalf("notified %d times, want one once it aired", rep.Notified)
	}
}

// A pack that states no range covers nothing in particular; announcing one
// claims an episode that may not exist yet.
func TestUnnumberedPackDoesNotAnnounceAnEpisode(t *testing.T) {
	st := airingStore(t, 0, 0, 5)

	rep := pollWith(t, st, []indexer.Torrent{
		release("2222222222222222222222222222222222222222",
			"[Group] Bleach - Sennen Kessen-hen - Kashin Tan [1080p][BD]", 90),
	})

	if rep.Notified != 0 {
		t.Errorf("notified %d times from a pack with no stated range", rep.Notified)
	}
}

// A pack that does state its range holds the episode, so it still counts.
func TestStatedPackStillAnnounces(t *testing.T) {
	st := airingStore(t, 0, 0, 5)

	rep := pollWith(t, st, []indexer.Torrent{
		release("3333333333333333333333333333333333333333",
			"[Group] Bleach - Sennen Kessen-hen - Kashin Tan (01-13) [1080p][Batch]", 90),
	})

	if rep.Notified != 1 {
		t.Errorf("notified %d times, want the stated range to count", rep.Notified)
	}
}

// A finished show has no airing data, and everything about it has aired.
func TestFollowAired(t *testing.T) {
	now := time.Now().Unix()
	tests := []struct {
		name    string
		follow  store.Follow
		episode int
		want    bool
	}{
		{"no airing data", store.Follow{}, 6, true},
		{"episode before the next one", store.Follow{NextEpisode: 6, NextAiringAt: now + 3600}, 5, true},
		{"the episode still to air", store.Follow{NextEpisode: 6, NextAiringAt: now + 3600}, 6, false},
		{"one past the next", store.Follow{NextEpisode: 6, NextAiringAt: now + 3600}, 7, false},
		{"airing time already passed", store.Follow{NextEpisode: 6, NextAiringAt: now - 60}, 6, true},
		{"episode but no time", store.Follow{NextEpisode: 6}, 6, true},
		{"time but no episode", store.Follow{NextAiringAt: now + 3600}, 6, true},
	}
	for _, tt := range tests {
		if got := tt.follow.Aired(tt.episode, now); got != tt.want {
			t.Errorf("%s: Aired(%d) = %v, want %v", tt.name, tt.episode, got, tt.want)
		}
	}
}
