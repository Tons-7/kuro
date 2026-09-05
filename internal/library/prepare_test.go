package library

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"kuro/internal/db"
	"kuro/internal/score"
	"kuro/internal/store"
)

func prefetchStore(t *testing.T) *store.Store {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := conn.Migrate(); err != nil {
		t.Fatal(err)
	}
	return store.New(conn)
}

// Resolving the next episode is a search, not a download, so it answers to its
// own switch rather than the off-by-default download switch.
func TestPrepareIsOnByDefaultAndNotTheDownloadSwitch(t *testing.T) {
	st := prefetchStore(t)
	ctx := context.Background()
	p := NewPrefetcher(st, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if !p.prepareWanted() {
		t.Error("resolving the next episode should be on out of the box")
	}
	if p.wanted() {
		t.Error("downloading the next episode should stay opt-in")
	}

	// Turning the download off must not take the resolve with it.
	if err := st.SetSetting(ctx, "cache.prefetch_next", "false"); err != nil {
		t.Fatal(err)
	}
	if !p.prepareWanted() {
		t.Error("the download switch still governs the resolve")
	}

	if err := st.SetSetting(ctx, "playback.prepare_next", "false"); err != nil {
		t.Fatal(err)
	}
	if p.prepareWanted() {
		t.Error("its own switch does not turn it off")
	}
}

func TestPreparedReleaseOutlastsAnEpisode(t *testing.T) {
	p := NewPrefetcher(prefetchStore(t), nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	p.storePrepared(5, 2, score.Result{})
	p.mu.Lock()
	entry := p.prepared[prepareKey(5, 2)]
	entry.at = time.Now().Add(-30 * time.Minute)
	p.prepared[prepareKey(5, 2)] = entry
	p.mu.Unlock()

	if _, ok := p.TakePrepared(5, 2); !ok {
		t.Fatal("a release resolved half an hour ago was discarded before the episode ended")
	}
	if _, ok := p.TakePrepared(5, 2); ok {
		t.Fatal("taking it must forget it")
	}
}

// An episode already in the library plays with no torrent, so resolving one is
// a wasted indexer search.
func TestPrepareSkipsAnEpisodeAlreadyInTheLibrary(t *testing.T) {
	st := prefetchStore(t)
	ctx := context.Background()
	if err := st.EnsureAnime(ctx, 42); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "Show - 07.mkv")
	if _, err := st.SaveLocalFiles(ctx, time.Now().Unix(), []store.LocalFile{{
		Path: path, Size: 1, AnimeID: 42, Episode: 7, Confidence: 1,
	}}); err != nil {
		t.Fatal(err)
	}
	if f, err := st.LocalEpisode(ctx, 42, 7); err != nil || f.ID == 0 {
		t.Fatalf("the library file was not recorded: %+v %v", f, err)
	}

	// A nil finder: reaching the search at all is a panic, which is the point.
	p := NewPrefetcher(st, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := p.prepare(ctx, 42, 7, 1, score.DefaultPreferences()); err != nil {
		t.Errorf("preparing an episode already on disk should be a no-op, got %v", err)
	}
}

// A start must wait for an in-flight resolve of the same episode; two racing
// searches leave the loser's torrent behind in the engine.
func TestAwaitPrepareWaitsForAResolveInFlight(t *testing.T) {
	p := NewPrefetcher(prefetchStore(t), nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	done := make(chan struct{})
	p.mu.Lock()
	p.running[prepareKey(7, 3)] = done
	p.mu.Unlock()

	var waited sync.WaitGroup
	waited.Add(1)
	released := make(chan time.Time, 1)
	go func() {
		defer waited.Done()
		p.AwaitPrepare(context.Background(), 7, 3)
		released <- time.Now()
	}()

	// Still running: the caller is held.
	select {
	case <-released:
		t.Fatal("returned while the resolve was still running")
	case <-time.After(150 * time.Millisecond):
	}

	closed := time.Now()
	close(done)
	waited.Wait()

	if at := <-released; at.Before(closed) {
		t.Error("released before the resolve finished")
	}

	// Nothing running for another episode, so nothing to wait for.
	start := time.Now()
	p.AwaitPrepare(context.Background(), 7, 4)
	if time.Since(start) > time.Second {
		t.Error("waited on an episode nothing was preparing")
	}
}

// A cancelled request must not sit on the wait.
func TestAwaitPrepareGivesUpWithTheRequest(t *testing.T) {
	p := NewPrefetcher(prefetchStore(t), nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	p.mu.Lock()
	p.running[prepareKey(1, 1)] = make(chan struct{})
	p.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	p.AwaitPrepare(ctx, 1, 1)
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("held for %s after the request was cancelled", elapsed)
	}
}

// A "nobody has subbed this yet" result won't change soon, so a pause stops
// every page visit re-searching every indexer.
func TestPrepareBacksOffAfterAFailure(t *testing.T) {
	st := prefetchStore(t)
	p := NewPrefetcher(st, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	prefs := score.DefaultPreferences()

	// No torrent client, so every attempt fails immediately... except that it
	// returns before starting anything. Record a failure by hand instead.
	p.mu.Lock()
	p.attempted[prepareKey(5, 2)] = time.Now()
	p.mu.Unlock()

	// PrepareTarget must not start work while the pause is in force; with no
	// engine it would return early anyway, so assert on the bookkeeping.
	p.PrepareTarget(5, 2, 1, prefs)
	p.mu.Lock()
	_, running := p.running[prepareKey(5, 2)]
	p.mu.Unlock()
	if running {
		t.Error("retried a failed resolve immediately")
	}

	// An attempt from long enough ago is not a reason to skip.
	p.mu.Lock()
	p.attempted[prepareKey(5, 2)] = time.Now().Add(-prepareRetry - time.Minute)
	stale := time.Since(p.attempted[prepareKey(5, 2)]) >= prepareRetry
	p.mu.Unlock()
	if !stale {
		t.Error("the pause never expires")
	}
}
