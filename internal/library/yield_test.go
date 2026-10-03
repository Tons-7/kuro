package library

import (
	"context"
	"testing"

	"kuro/internal/indexer"
	"kuro/internal/score"
	"kuro/internal/store"
)

// A background download must not starve the episode starting.
func TestStartingEpisodeTakesTheLine(t *testing.T) {
	engine := newFakeEngine()
	p := newPlayback(t, engine, []indexer.Torrent{
		release(goodHash, "[Live] Sousou no Frieren - 01 [1080p].mkv", 50),
	})
	ctx := context.Background()

	const finishing, kept = "5555555555555555555555555555555555555555", "6666666666666666666666666666666666666666"
	other, keeper := 2, 3
	if _, err := p.store.ImportList(ctx, []store.Anime{
		{ID: other, Romaji: "Monster", Synonyms: "[]", Genres: "[]"},
		{ID: keeper, Romaji: "Mushishi", Synonyms: "[]", Genres: "[]"},
	}, nil, store.ImportMerge); err != nil {
		t.Fatal(err)
	}
	for id, rec := range map[int]store.TorrentRecord{
		50: {InfoHash: finishing, EngineID: 50, Name: "Monster - 02", AnimeID: other, EpKey: "2"},
		51: {InfoHash: kept, EngineID: 51, Name: "Mushishi - 01", AnimeID: keeper, EpKey: "1"},
	} {
		engine.ids[id] = rec.InfoHash
		if err := p.store.RecordTorrent(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := p.store.KeepDownload(ctx, kept, true); err != nil {
		t.Fatal(err)
	}

	if _, err := p.Start(ctx, PlayRequest{AnimeID: 1, Episode: 1, Season: 1, Prefs: score.DefaultPreferences()}); err != nil {
		t.Fatalf("start: %v", err)
	}
	if !engine.isPaused(50) {
		t.Error("another show's background download kept running")
	}
	if engine.isPaused(51) {
		t.Error("a kept download was paused; the queue manages those")
	}

	// Episode 2 now plays; leaving episode 1 must not hand the line back.
	const next = "7777777777777777777777777777777777777777"
	if err := p.store.RecordTorrent(ctx, store.TorrentRecord{InfoHash: next, EngineID: 52, Name: "Frieren - 02", AnimeID: 1, EpKey: "2"}); err != nil {
		t.Fatal(err)
	}
	if err := p.store.PinCache(ctx, next, 0, true); err != nil {
		t.Fatal(err)
	}
	p.Suspend(ctx, 1, 1)
	if !engine.isPaused(50) {
		t.Error("leaving one episode resumed downloads while another plays")
	}

	p.Suspend(ctx, 1, 2)
	if engine.isPaused(50) {
		t.Error("the background download was not resumed after leaving")
	}
}
