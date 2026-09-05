package library

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	"kuro/internal/indexer"
	"kuro/internal/score"
	"kuro/internal/store"
	"kuro/internal/torrent"
)

// Downloading an episode already held promotes that copy to kept instead of
// fetching a second one.
func TestDownloadPromotesTheCachedCopy(t *testing.T) {
	engine := newFakeRqbit()
	srv := httptest.NewServer(engine.handler())
	t.Cleanup(srv.Close)

	st := prefetchStore(t)
	ctx := context.Background()
	if err := st.RecordTorrent(ctx, store.TorrentRecord{
		InfoHash: goodHash, Name: "cached", AnimeID: 1, EpKey: "1",
		FilePath: "cached.mkv", TotalSize: 100,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetCacheBytes(ctx, goodHash, 0, 100, true); err != nil {
		t.Fatal(err)
	}

	// A nil finder: reaching the search at all is a panic, which is the point.
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	p := NewPrefetcher(st, nil, torrent.NewClient(srv.URL), log)
	if err := p.Download(ctx, 1, 1, 1, score.DefaultPreferences(), time.Second); err != nil {
		t.Fatalf("download: %v", err)
	}

	engine.mu.Lock()
	added := len(engine.added)
	engine.mu.Unlock()
	if added != 0 {
		t.Errorf("added %d torrents for an episode already held", added)
	}
	entries, _ := st.CacheEntries(ctx)
	if len(entries) != 1 || !entries[0].Kept {
		t.Fatalf("entries = %+v, want the held copy kept", entries)
	}
}

// An asked-for download is outside the cache budget, so a full cache refuses a
// prefetch but not a download.
func TestDownloadIgnoresTheCacheBudget(t *testing.T) {
	engine := newFakeRqbit()
	srv := httptest.NewServer(engine.handler())
	t.Cleanup(srv.Close)

	st := prefetchStore(t)
	ctx := context.Background()
	episodes := 28
	st.ImportList(ctx,
		[]store.Anime{{ID: 1, Romaji: "Sousou no Frieren", Synonyms: "[]", Genres: "[]", Episodes: &episodes}},
		nil, store.ImportMerge)
	if err := st.SetSetting(ctx, "cache.budget_bytes", "1"); err != nil {
		t.Fatal(err)
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	finder := NewFinder(st, fixedIndexer{results: []indexer.Torrent{
		release(goodHash, "[Best] Sousou no Frieren - 01 [1080p BluRay].mkv", 900),
	}}, log)
	p := NewPrefetcher(st, finder, torrent.NewClient(srv.URL), log)
	prefs := score.DefaultPreferences()

	if _, _, err := p.fetch(ctx, 1, 1, 1, prefs, false); err == nil {
		t.Fatal("a prefetch into a full cache was not refused")
	}
	_, started, err := p.fetch(ctx, 1, 1, 1, prefs, true)
	if err != nil || !started {
		t.Fatalf("download refused: started=%v err=%v", started, err)
	}

	usage, err := st.CacheUsage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if usage.Kept != 1 || usage.Bytes != 0 {
		t.Errorf("usage = %+v, want the download kept and outside the budget", usage)
	}
}

// A held release that no longer delivers is replaced on play; the replacement
// inherits its keep and the dead one leaves the engine instead of lingering.
func TestStartReplacesADeadHeldRelease(t *testing.T) {
	engine := newFakeRqbit(deadHash)
	p := newPlayback(t, engine, []indexer.Torrent{
		release(goodHash, "[Best] Sousou no Frieren - 01 [1080p].mkv", 900),
	})
	ctx := context.Background()

	engine.mu.Lock()
	engine.ids[9] = deadHash
	engine.mu.Unlock()
	if err := p.store.RecordTorrent(ctx, store.TorrentRecord{
		InfoHash: deadHash, RqbitID: 9, Name: "dead", AnimeID: 1, EpKey: "1",
		FilePath: "dead.mkv", TotalSize: 100,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.store.KeepDownload(ctx, deadHash, true); err != nil {
		t.Fatal(err)
	}

	session, err := p.Start(ctx, PlayRequest{AnimeID: 1, Episode: 1, Season: 1, Prefs: score.DefaultPreferences()})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if session.InfoHash != goodHash {
		t.Fatalf("played %s, want the live release", session.InfoHash)
	}
	if !engine.wasDeleted(deadHash) {
		t.Error("the dead release is still in the engine")
	}

	entries, _ := p.store.CacheEntries(ctx)
	if len(entries) != 1 || entries[0].InfoHash != goodHash || !entries[0].Kept {
		t.Fatalf("entries = %+v, want only the replacement, kept", entries)
	}
}
