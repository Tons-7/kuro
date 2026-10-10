package library

import (
	"context"
	"testing"

	"kuro/internal/store"
)

func pausableQueue(t *testing.T) (*Downloader, *store.Store) {
	t.Helper()
	st := prefetchStore(t)
	episodes := 12
	if _, err := st.ImportList(context.Background(),
		[]store.Anime{{ID: 1, Romaji: "Show", Synonyms: "[]", Genres: "[]", Episodes: &episodes}},
		nil, store.ImportMerge); err != nil {
		t.Fatal(err)
	}
	return NewDownloader(st, NewPrefetcher(st, nil, nil, discard()), nil, discard()), st
}

// Pausing one row lets the next in line start; pausing the queue is the way to stop them all.
func TestPausedQueueStartsNothingUntilResumed(t *testing.T) {
	d, _ := pausableQueue(t)
	ctx := context.Background()
	if d.held() || d.Paused() {
		t.Fatal("a new queue starts out paused")
	}

	if err := d.SetPaused(ctx, true); err != nil {
		t.Fatal(err)
	}
	if !d.held() || !d.Paused() {
		t.Error("a paused queue would still start the next download")
	}

	if err := d.SetPaused(ctx, false); err != nil {
		t.Fatal(err)
	}
	if d.held() {
		t.Error("a resumed queue is still held")
	}
	// Resuming wakes the worker rather than leaving it to its next idle tick.
	select {
	case <-d.wake:
	default:
		t.Error("resuming did not wake the worker")
	}
}

// Pausing also stops the download in flight, which goes back to wait its turn.
func TestPausingTheQueueStopsTheDownloadInFlight(t *testing.T) {
	d, _ := pausableQueue(t)
	stopped := false
	d.current = &inFlight{animeID: 1, epKey: "3", stop: func() { stopped = true }}

	if err := d.SetPaused(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if !stopped {
		t.Error("the download in flight kept going under a paused queue")
	}
}

// A restart must not start downloading again by itself.
func TestPausedQueueStaysPausedAcrossARestart(t *testing.T) {
	d, st := pausableQueue(t)
	ctx := context.Background()
	if err := d.SetPaused(ctx, true); err != nil {
		t.Fatal(err)
	}

	again := NewDownloader(st, NewPrefetcher(st, nil, nil, discard()), nil, discard())
	again.restorePaused(ctx)
	if !again.Paused() {
		t.Error("the pause was forgotten")
	}

	if err := again.SetPaused(ctx, false); err != nil {
		t.Fatal(err)
	}
	third := NewDownloader(st, NewPrefetcher(st, nil, nil, discard()), nil, discard())
	third.restorePaused(ctx)
	if third.Paused() {
		t.Error("a resumed queue came back paused")
	}
}

// "Download now" puts the episode first, stops the one in flight, and resumes a paused queue.
func TestDownloadNowJumpsTheLineAndResumes(t *testing.T) {
	d, st := pausableQueue(t)
	ctx := context.Background()
	if _, err := st.Enqueue(ctx, 1, 1, []int{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	active, _, err := st.NextQueued(ctx)
	if err != nil || active.Episode != 1 {
		t.Fatalf("active %+v, err %v", active, err)
	}
	stopped := false
	d.current = &inFlight{animeID: 1, epKey: "1", stop: func() { stopped = true }}
	if err := d.SetPaused(ctx, true); err != nil {
		t.Fatal(err)
	}
	stopped = false

	moved, err := d.Now(ctx, 1, "3")
	if err != nil || !moved {
		t.Fatalf("moved %v, err %v", moved, err)
	}
	if !stopped || d.Paused() {
		t.Errorf("in flight stopped: %v, queue still paused: %v", stopped, d.Paused())
	}

	// The worker requeues what it stopped; the asked-for episode is then first, the stopped one right behind.
	if _, err := st.RequeueActive(ctx, 1, "1"); err != nil {
		t.Fatal(err)
	}
	first, _, _ := st.NextQueued(ctx)
	second, _, _ := st.NextQueued(ctx)
	if first.Episode != 3 || second.Episode != 1 {
		t.Errorf("order after download now: %d then %d, want 3 then 1", first.Episode, second.Episode)
	}
}

// An episode that is not waiting cannot be started now, and nothing is disturbed for it.
func TestDownloadNowIgnoresWhatIsNotWaiting(t *testing.T) {
	d, _ := pausableQueue(t)
	stopped := false
	d.current = &inFlight{animeID: 1, epKey: "1", stop: func() { stopped = true }}

	moved, err := d.Now(context.Background(), 1, "9")
	if err != nil || moved || stopped {
		t.Errorf("moved %v, stopped %v, err %v", moved, stopped, err)
	}
}
