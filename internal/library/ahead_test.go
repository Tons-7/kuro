package library

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"

	"kuro/internal/indexer"
	"kuro/internal/score"
	"kuro/internal/store"
	"kuro/internal/torrent"
)

// aheadPrefetcher holds one show whose indexer remembers every search.
func aheadPrefetcher(t *testing.T, engine *fakeEngine, results ...indexer.Torrent) (*Prefetcher, *askedIndexer, *store.Store) {
	t.Helper()
	srv := httptest.NewServer(engine.handler())
	t.Cleanup(srv.Close)

	st := prefetchStore(t)
	episodes := 28
	if _, err := st.ImportList(context.Background(),
		[]store.Anime{{ID: 1, Romaji: "Sousou no Frieren", Synonyms: "[]", Genres: "[]", Episodes: &episodes}},
		nil, store.ImportMerge); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	idx := &askedIndexer{results: results}
	return NewPrefetcher(st, NewFinder(st, idx, log), torrent.NewClient(srv.URL), log), idx, st
}

func (a *askedIndexer) searches() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.asked)
}

// A queued episode resolved while the one before it downloaded starts without searching again.
func TestDownloadUsesTheReleaseResolvedAhead(t *testing.T) {
	engine := newFakeEngine()
	p, idx, _ := aheadPrefetcher(t, engine, release(goodHash, "[Best] Sousou no Frieren - 01 [1080p BluRay].mkv", 900))
	ctx := context.Background()
	prefs := score.DefaultPreferences()

	if err := p.prepare(ctx, 1, 1, 1, prefs); err != nil {
		t.Fatal(err)
	}
	resolved := idx.searches()
	if resolved == 0 {
		t.Fatal("resolving ahead searched nothing")
	}

	_, started, err := p.fetch(ctx, 1, 1, 1, prefs, true)
	if err != nil || !started {
		t.Fatalf("download: started=%v err=%v", started, err)
	}
	if again := idx.searches() - resolved; again != 0 {
		t.Errorf("searched %d more times for a release already resolved", again)
	}
	engine.mu.Lock()
	added := len(engine.added)
	engine.mu.Unlock()
	if added != 1 {
		t.Errorf("added %d torrents, want the resolved one", added)
	}
}

// What was resolved ahead may be gone by its turn; the download then searches as it always did.
func TestDownloadSearchesWhenTheResolvedReleaseIsDead(t *testing.T) {
	engine := newFakeEngine(deadHash)
	p, idx, st := aheadPrefetcher(t, engine, release(goodHash, "[Live] Sousou no Frieren - 01 [1080p].mkv", 50))
	ctx := context.Background()

	p.storePrepared(1, 1, score.Result{
		Candidate: score.Candidate{Torrent: release(deadHash, "[Dead] Sousou no Frieren - 01 [1080p].mkv", 900), Numbers: []int{1}},
		AutoPick:  true,
	})
	_, started, err := p.fetch(ctx, 1, 1, 1, score.DefaultPreferences(), true)
	if err != nil || !started {
		t.Fatalf("download: started=%v err=%v", started, err)
	}
	if idx.searches() == 0 {
		t.Error("the dead release was not followed by a search")
	}
	if rec, ok, _ := st.TorrentForEpisode(ctx, 1, "1"); !ok || rec.InfoHash != goodHash {
		t.Errorf("recorded %+v, want the live release", rec)
	}
}

// A prefetch refused for space must not cost play the release it had ready for that episode.
func TestPrefetchRefusedForSpaceKeepsTheResolvedRelease(t *testing.T) {
	engine := newFakeEngine()
	p, _, st := aheadPrefetcher(t, engine, release(goodHash, "[Best] Sousou no Frieren - 01 [1080p BluRay].mkv", 900))
	ctx := context.Background()
	prefs := score.DefaultPreferences()
	if err := p.prepare(ctx, 1, 1, 1, prefs); err != nil {
		t.Fatal(err)
	}
	// Under the budget now, over it with the episode.
	if err := st.SetSetting(ctx, "cache.budget_bytes", "1024"); err != nil {
		t.Fatal(err)
	}

	if _, started, err := p.fetch(ctx, 1, 1, 1, prefs, false); err == nil || started {
		t.Fatalf("a prefetch over the budget went ahead: started=%v err=%v", started, err)
	}
	if _, ok := p.TakePrepared(1, 1); !ok {
		t.Error("the refused prefetch used up the release play was going to start with")
	}
}

// With a cache to ask, a prefetch that does not fit asks for room for that show and goes ahead.
func TestPrefetchAsksTheCacheForRoom(t *testing.T) {
	engine := newFakeEngine()
	p, _, st := aheadPrefetcher(t, engine, release(goodHash, "[Best] Sousou no Frieren - 01 [1080p BluRay].mkv", 900))
	ctx := context.Background()
	if err := st.SetSetting(ctx, "cache.budget_bytes", "1024"); err != nil {
		t.Fatal(err)
	}
	var askedFor, askedBytes int64
	p.WithRoom(func(_ context.Context, need int64, animeID int) bool {
		askedFor, askedBytes = int64(animeID), need
		return true
	})

	if _, started, err := p.fetch(ctx, 1, 1, 1, score.DefaultPreferences(), false); err != nil || !started {
		t.Fatalf("prefetch with room made: started=%v err=%v", started, err)
	}
	if askedFor != 1 || askedBytes != 1<<30 {
		t.Errorf("asked for %d bytes for show %d, want the episode's size for show 1", askedBytes, askedFor)
	}
}

// Ahead resolves in the background whatever the playback switch says, and never touches the engine.
func TestAheadResolvesWithoutDownloading(t *testing.T) {
	engine := newFakeEngine()
	p, _, st := aheadPrefetcher(t, engine, release(goodHash, "[Best] Sousou no Frieren - 01 [1080p BluRay].mkv", 900))
	ctx := context.Background()
	if err := st.SetSetting(ctx, "playback.prepare_next", "false"); err != nil {
		t.Fatal(err)
	}

	p.Ahead(1, 1, 1, score.DefaultPreferences())
	p.AwaitPrepare(ctx, 1, 1)

	engine.mu.Lock()
	added := len(engine.added)
	engine.mu.Unlock()
	if added != 0 {
		t.Errorf("resolving ahead added %d torrents", added)
	}
	if rel, ok := p.TakePrepared(1, 1); !ok || rel.Torrent.InfoHash != goodHash {
		t.Errorf("resolved %+v, ok %v", rel.Torrent, ok)
	}
}
