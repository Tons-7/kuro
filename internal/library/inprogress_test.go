package library

import (
	"context"
	"testing"

	"kuro/internal/store"
)

// A finished film bigger than the budget, played a little and left for later: the
// case that lost someone a film an hour after it finished downloading.
func filmInCache(t *testing.T) (*Cache, *store.Store) {
	t.Helper()
	c, st := newCache(t)
	ctx := context.Background()
	if err := st.RecordTorrent(ctx, store.TorrentRecord{
		InfoHash: "film", Name: "Film.mkv", AnimeID: 1, EpKey: "1", FilePath: "Film.mkv", TotalSize: 8 << 30,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetCacheBytes(ctx, "film", 0, 8<<30, true); err != nil {
		t.Fatal(err)
	}
	if err := st.PinCache(ctx, "film", 0, false); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(ctx, "cache.budget_bytes", "5368709120"); err != nil {
		t.Fatal(err)
	}
	return c, st
}

func played(t *testing.T, st *store.Store, animeID int, position float64) bool {
	t.Helper()
	watched, err := st.SavePlayback(context.Background(), store.PlaybackState{
		AnimeID: animeID, EpKey: "1", Position: position, Duration: 1400, Played: 120,
	})
	if err != nil {
		t.Fatal(err)
	}
	return watched
}

func stillThere(t *testing.T, st *store.Store, hash string) bool {
	t.Helper()
	entries, err := st.CacheEntries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.InfoHash == hash {
			return true
		}
	}
	return false
}

func TestEvictionOrderNeverOffersTheEpisodeBeingWatched(t *testing.T) {
	one, two := 1, 2
	film := entry("film", 8<<30, 10, false, false)
	film.AnimeID, film.EpKey = &one, "1"
	other := entry("other", 1<<30, 500, false, false)
	other.AnimeID, other.EpKey = &two, "3"

	got := evictionOrder([]store.CacheEntry{film, other}, nil, episodeRef{animeID: 1, epKey: "1"})
	if len(got) != 1 || got[0].InfoHash != "other" {
		t.Fatalf("order = %+v, want only the other episode", got)
	}
}

func TestSweepSparesTheEpisodeBeingWatched(t *testing.T) {
	c, st := filmInCache(t)
	ctx := context.Background()
	if played(t, st, 1, 120) {
		t.Fatal("two minutes in counted as watched")
	}

	rep, err := c.Sweep(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Evicted != 0 || !stillThere(t, st, "film") {
		t.Fatalf("the film being watched was evicted (report %+v)", rep)
	}
	// Over budget is the right answer while the film is in progress.
	if rep.After <= rep.Budget {
		t.Errorf("after = %d, expected to stay over the %d budget", rep.After, rep.Budget)
	}
}

// Finished, it is an ordinary cached episode again and the budget applies.
func TestSweepTakesTheFilmOnceItIsWatched(t *testing.T) {
	c, st := filmInCache(t)
	ctx := context.Background()
	watched := false
	for i := 0; i < 8 && !watched; i++ {
		watched = played(t, st, 1, 1390)
	}
	if !watched {
		t.Fatal("playing the film through never counted as watched")
	}

	if rep, _ := c.Sweep(ctx); rep.Evicted != 1 || stillThere(t, st, "film") {
		t.Fatalf("a watched film over budget stayed (report %+v)", rep)
	}
}

func TestSweepTakesTheFilmOnceDismissedFromContinueWatching(t *testing.T) {
	c, st := filmInCache(t)
	ctx := context.Background()
	played(t, st, 1, 120)
	if err := st.DismissResume(ctx, 1, "1"); err != nil {
		t.Fatal(err)
	}

	if rep, _ := c.Sweep(ctx); rep.Evicted != 1 || stillThere(t, st, "film") {
		t.Fatalf("a dismissed film over budget stayed (report %+v)", rep)
	}
}

// Only the episode last played is spared: moving on to another show gives the film back to the budget.
func TestSweepTakesTheFilmOncePlayingSomethingElse(t *testing.T) {
	c, st := filmInCache(t)
	ctx := context.Background()
	played(t, st, 1, 120)
	if err := st.EnsureAnime(ctx, 2); err != nil {
		t.Fatal(err)
	}
	played(t, st, 2, 300)

	if rep, _ := c.Sweep(ctx); rep.Evicted != 1 || stillThere(t, st, "film") {
		t.Fatalf("the film stayed after another show was played (report %+v)", rep)
	}
}
