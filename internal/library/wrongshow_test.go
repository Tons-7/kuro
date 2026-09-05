package library

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"kuro/internal/indexer"
	"kuro/internal/score"
	"kuro/internal/store"
	"kuro/internal/torrent"
)

func monsterStore(t *testing.T) *store.Store {
	t.Helper()
	st := prefetchStore(t)
	episodes := 74
	if _, err := st.ImportList(context.Background(),
		[]store.Anime{{
			ID: 19, Romaji: "MONSTER", Synonyms: `["Potwór"]`,
			Genres: "[]", Episodes: &episodes,
		}}, nil, store.ImportMerge); err != nil {
		t.Fatal(err)
	}
	return st
}

func findMonster(t *testing.T, results []indexer.Torrent, episode int) Candidates {
	t.Helper()
	st := monsterStore(t)
	f := NewFinder(st, fixedIndexer{results: results},
		slog.New(slog.NewTextHandler(io.Discard, nil)))

	got, err := f.Find(context.Background(), Request{
		AnimeID: 19, Episode: episode, Season: 1, Prefs: score.DefaultPreferences(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// The searches for a one-word title return other shows, and every one of them
// used to outrank the show itself for being newer and higher resolution.
func TestOtherShowsNeverWinTheEpisode(t *testing.T) {
	got := findMonster(t, []indexer.Torrent{
		release("1111111111111111111111111111111111111111",
			"[Erai-raws] Monogatari Series - Off and Monster Season - 01 [1080p][Multiple Subtitle]", 900),
		release("2222222222222222222222222222222222222222",
			"[Erai-raws] Re-Monster - 01 [1080p][Multiple Subtitle]", 800),
		release("3333333333333333333333333333333333333333",
			"[sam] Monster (2004) - 01 (DVD 572p HEVC x265 10-bit AC-3) [Dual-Audio]", 48),
	}, 1)

	if got.Best == nil {
		t.Fatal("nothing was picked; the show's own release should have been")
	}
	if !strings.Contains(got.Best.Torrent.Title, "[sam] Monster (2004)") {
		t.Fatalf("picked %q, want the show itself", got.Best.Torrent.Title)
	}
}

// Rejected releases stay in the list so the manual picker can show what was
// found, with the reason attached.
func TestOtherShowsAreKeptButBlocked(t *testing.T) {
	got := findMonster(t, []indexer.Torrent{
		release("1111111111111111111111111111111111111111",
			"[EMBER] Monster Girl Doctor (2020) (Season 1) [BDRip] [1080p Dual Audio HEVC]", 900),
		release("3333333333333333333333333333333333333333",
			"[CBM] Monster 1-74 Complete (Dual Audio) [DVDRip-480p-8bit]", 200),
	}, 30)

	if len(got.Results) != 2 {
		t.Fatalf("got %d results, want both kept for the picker", len(got.Results))
	}

	var blocked, ok int
	for _, r := range got.Results {
		if strings.Contains(r.Torrent.Title, "Monster Girl Doctor") {
			blocked++
			if r.AutoPick {
				t.Error("another show was eligible for automatic selection")
			}
			if !strings.Contains(r.Blocked, "different show") {
				t.Errorf("blocked = %q, want it to name the real problem", r.Blocked)
			}
			continue
		}
		ok++
		if !r.AutoPick {
			t.Errorf("the show's own release was blocked: %q", r.Blocked)
		}
	}
	if blocked != 1 || ok != 1 {
		t.Fatalf("blocked %d and allowed %d, want one of each", blocked, ok)
	}
}

// A twelve episode series carries no episode 30, and a pack that states no
// range used to be allowed to answer for any number at all.
func TestUnnumberedPackOfAnotherShowCannotAnswer(t *testing.T) {
	got := findMonster(t, []indexer.Torrent{
		release("1111111111111111111111111111111111111111",
			"[ZeroBuild] Re:Monster (S01) (Season 1) (BD 1080p HEVC 10-bit Opus) [Dual Audio]", 900),
	}, 30)

	if got.Best != nil {
		t.Fatalf("picked %q for an episode it cannot hold", got.Best.Torrent.Title)
	}
}

// Nothing found is better than the wrong show: an episode with no release must
// report that rather than play something else.
func TestNothingIsPickedWhenOnlyOtherShowsAreFound(t *testing.T) {
	got := findMonster(t, []indexer.Torrent{
		release("1111111111111111111111111111111111111111",
			"[somedroplet] Pokémon Horizons / Pocket Monsters (2023) - 074", 900),
		release("2222222222222222222222222222222222222222",
			"[flowernal] Pocket Monsters (2023) - 074v2 [1080p] [HEVC]", 700),
	}, 74)

	if got.Best != nil {
		t.Fatalf("picked %q, want nothing", got.Best.Torrent.Title)
	}
	if len(got.Results) != 2 {
		t.Errorf("got %d results, want them kept for the picker", len(got.Results))
	}
}

func TestHeldReleaseOfAnotherShowIsNotReused(t *testing.T) {
	st := monsterStore(t)
	ctx := context.Background()

	if err := st.RecordTorrent(ctx, store.TorrentRecord{
		InfoHash: "1111111111111111111111111111111111111111",
		Name:     "[Erai-raws] Monogatari Series - Off and Monster Season - 01 [1080p]",
		AnimeID:  19, EpKey: "1", FilePath: "wrong.mkv", TotalSize: 100,
	}); err != nil {
		t.Fatal(err)
	}
	if heldNamesShow(ctx, st, 19, "[Erai-raws] Monogatari Series - Off and Monster Season - 01 [1080p]") {
		t.Error("a recorded release of another show was accepted as held")
	}
	if !heldNamesShow(ctx, st, 19, "[sam] Monster (2004) - 01 (DVD 572p)") {
		t.Error("the show's own held release was rejected")
	}
}

// Download used to promote whatever was held to kept, then, finding it named
// another show, return as if done: the wrong file pinned, nothing fetched.
func TestDownloadFetchesAfreshWhenTheHeldReleaseIsAnotherShow(t *testing.T) {
	engine := newFakeRqbit()
	srv := httptest.NewServer(engine.handler())
	t.Cleanup(srv.Close)

	st := monsterStore(t)
	ctx := context.Background()
	wrong := "1111111111111111111111111111111111111111"
	if err := st.RecordTorrent(ctx, store.TorrentRecord{
		InfoHash: wrong, Name: "[Erai-raws] Monogatari Series - Off and Monster Season - 01 [1080p]",
		AnimeID: 19, EpKey: "1", FilePath: "wrong.mkv", TotalSize: 100,
	}); err != nil {
		t.Fatal(err)
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	finder := NewFinder(st, fixedIndexer{results: []indexer.Torrent{
		release("3333333333333333333333333333333333333333",
			"[sam] Monster (2004) - 01 (DVD 572p HEVC x265 10-bit AC-3) [Dual-Audio]", 48),
	}}, log)
	p := NewPrefetcher(st, finder, torrent.NewClient(srv.URL), log)
	p.Download(ctx, 19, 1, 1, score.DefaultPreferences(), time.Second)

	engine.mu.Lock()
	added := len(engine.added)
	engine.mu.Unlock()
	if added != 1 {
		t.Errorf("added %d torrents, want the real episode fetched", added)
	}
	entries, _ := st.CacheEntries(ctx)
	for _, e := range entries {
		if e.InfoHash == wrong && e.Kept {
			t.Error("the other show's file was pinned as kept")
		}
	}
}

// The queue used to commit to the best-ranked release and fail with it.
func TestDownloadWalksPastADeadRelease(t *testing.T) {
	engine := newFakeRqbit(deadHash)
	srv := httptest.NewServer(engine.handler())
	t.Cleanup(srv.Close)

	st := monsterStore(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	finder := NewFinder(st, fixedIndexer{results: []indexer.Torrent{
		release(deadHash, "[sam] Monster (2004) - 01 (DVD 572p HEVC x265 10-bit AC-3) [Dual-Audio]", 900),
		release(goodHash, "[Other] Monster (2004) - 01 [1080p].mkv", 40),
	}}, log)
	p := NewPrefetcher(st, finder, torrent.NewClient(srv.URL), log)

	p.Download(context.Background(), 19, 1, 1, score.DefaultPreferences(), time.Second)

	engine.mu.Lock()
	added := append([]string{}, engine.added...)
	engine.mu.Unlock()
	if len(added) != 1 || !strings.Contains(added[0], goodHash) {
		t.Fatalf("added = %v, want the live release after the dead one failed", added)
	}
}

// The picker exists for releases the heuristic rejects; one chosen there has
// to stick, or every play would send the user back to it.
func TestAReleasePickedByHandIsNeverSecondGuessed(t *testing.T) {
	st := monsterStore(t)
	ctx := context.Background()
	if err := st.RecordTorrent(ctx, store.TorrentRecord{
		InfoHash: "1111111111111111111111111111111111111111",
		Name:     "[Group] Some Naming AniList Never Heard Of - 01 [1080p]",
		AnimeID:  19, EpKey: "1", FilePath: "ep1.mkv", TotalSize: 100, Manual: true,
	}); err != nil {
		t.Fatal(err)
	}

	p := NewPrefetcher(st, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, ok := p.held(ctx, 19, 1); !ok {
		t.Error("a hand-picked release was discarded for its name")
	}

	// Re-recorded by an automatic path later, it stays a hand pick.
	if err := st.RecordTorrent(ctx, store.TorrentRecord{
		InfoHash: "1111111111111111111111111111111111111111",
		Name:     "[Group] Some Naming AniList Never Heard Of - 01 [1080p]",
		AnimeID:  19, EpKey: "1", FilePath: "ep1.mkv", TotalSize: 100,
	}); err != nil {
		t.Fatal(err)
	}
	rec, ok, err := st.TorrentForEpisode(ctx, 19, "1")
	if err != nil || !ok || !rec.Manual {
		t.Fatalf("record = %+v, %v, %v, want the manual flag kept", rec, ok, err)
	}
}

// Without titles there is nothing to check against, and refusing everything
// would strand playback.
func TestHeldReleaseStandsWithoutTitles(t *testing.T) {
	st := prefetchStore(t)
	ctx := context.Background()

	if !heldNamesShow(ctx, st, 404, "anything at all") {
		t.Error("an unknown show should not invalidate what is held")
	}
	if !heldNamesShow(ctx, st, 404, "") {
		t.Error("an unnamed record should stand")
	}

	// A placeholder row has a title in name only.
	if err := st.EnsureAnime(ctx, 405); err != nil {
		t.Fatal(err)
	}
	if !heldNamesShow(ctx, st, 405, "[Group] Whatever - 01 [1080p].mkv") {
		t.Error("the Unknown placeholder was taken as evidence")
	}
}
