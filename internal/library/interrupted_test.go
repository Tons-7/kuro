package library

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"kuro/internal/indexer"
	"kuro/internal/score"
	"kuro/internal/store"
	"kuro/internal/torrent"
)

// A download stopped before its first bytes (an episode started playing) stays this episode's, so the
// retry resumes it: unrecorded, the sweep listed it under no show and the queue searched again.
func TestInterruptedDownloadIsStillTheEpisodes(t *testing.T) {
	engine := newFakeEngine()
	engine.stuck[goodHash] = true
	srv := httptest.NewServer(engine.handler())
	t.Cleanup(srv.Close)

	st := prefetchStore(t)
	ctx := context.Background()
	// Named as the fake engine names its file, so the held copy is recognised as the show's.
	episodes, english := 28, "Sousou no Frieren"
	if _, err := st.ImportList(ctx,
		[]store.Anime{{ID: 1, Romaji: "Frieren", English: &english, Synonyms: "[]", Genres: "[]", Episodes: &episodes}},
		nil, store.ImportMerge); err != nil {
		t.Fatal(err)
	}
	finder := NewFinder(st, fixedIndexer{results: []indexer.Torrent{
		release(goodHash, "[Best] Sousou no Frieren - 01 [1080p].mkv", 900),
	}}, discard())
	p := NewPrefetcher(st, finder, torrent.NewClient(srv.URL), discard())

	stop, cancel := context.WithCancel(ctx)
	go func() { time.Sleep(400 * time.Millisecond); cancel() }()
	err := p.Download(stop, 1, 1, 1, score.DefaultPreferences(), time.Minute)
	if err == nil {
		t.Fatal("a download whose first bytes never came finished")
	}
	if stop.Err() == nil {
		t.Fatalf("the download gave up on its own: %v", err)
	}

	rec, ok, err := st.TorrentForEpisode(ctx, 1, "1")
	if err != nil || !ok || rec.InfoHash != goodHash {
		t.Fatalf("the interrupted download is not on record for episode 1: ok=%v rec=%+v err=%v", ok, rec, err)
	}
	if _, held := p.held(ctx, 1, 1); !held {
		t.Error("the retry would search again instead of resuming the torrent it holds")
	}
}

// Playing an episode already whole on disk pauses nothing; "continue" pauses nothing ever.
func TestYieldOnlyForAnEpisodeStillArriving(t *testing.T) {
	engine := newFakeEngine()
	p := newPlayback(t, engine, nil)
	ctx := context.Background()
	if err := p.store.EnsureAnime(ctx, 2); err != nil {
		t.Fatal(err)
	}
	// Another show's download, half way.
	engine.ids[5] = deadHash
	if err := p.store.RecordTorrent(ctx, store.TorrentRecord{
		InfoHash: deadHash, EngineID: 5, Name: "other", AnimeID: 2, EpKey: "1", FilePath: "other.mkv", TotalSize: 100,
	}); err != nil {
		t.Fatal(err)
	}
	if err := p.store.PinCache(ctx, deadHash, 0, false); err != nil {
		t.Fatal(err)
	}
	// This show's episode, whole on disk.
	engine.ids[6] = goodHash
	if err := p.store.RecordTorrent(ctx, store.TorrentRecord{
		InfoHash: goodHash, EngineID: 6, Name: "mine", AnimeID: 1, EpKey: "1", FilePath: "mine.mkv", TotalSize: 100,
	}); err != nil {
		t.Fatal(err)
	}
	if err := p.store.SetCacheBytes(ctx, goodHash, 0, 100, true); err != nil {
		t.Fatal(err)
	}

	p.yield(ctx, 1, 1)
	if engine.isPaused(5) {
		t.Fatal("playing an episode already on disk paused another show's download")
	}

	p.yield(ctx, 1, 2)
	if !engine.isPaused(5) {
		t.Fatal("an episode still to arrive did not get the line")
	}

	engine.paused[5] = false
	if err := p.store.SetSetting(ctx, "downloads.while_watching", "continue"); err != nil {
		t.Fatal(err)
	}
	p.yield(ctx, 1, 2)
	if engine.isPaused(5) {
		t.Fatal("downloads paused under \"never pause\"")
	}
}
