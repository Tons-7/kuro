package store

import (
	"context"
	"errors"
	"testing"
)

// Downloads run one at a time across every show, so the queue has to hand out
// work in the order it was asked for and survive being asked twice.
func TestQueueIsWorkedInOrder(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedCatalogue(t, s, 100, "FINISHED", 12)

	added, err := s.Enqueue(ctx, 100, 1, []int{1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	if added != 3 {
		t.Fatalf("queued %d, want 3", added)
	}

	// Asking again is harmless: nothing is duplicated.
	if _, err := s.Enqueue(ctx, 100, 1, []int{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	items, err := s.QueuedDownloads(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("queue holds %d, want 3", len(items))
	}

	for want := 1; want <= 3; want++ {
		next, ok, err := s.NextQueued(ctx)
		if err != nil || !ok {
			t.Fatalf("episode %d: ok=%v err=%v", want, ok, err)
		}
		if next.Episode != want {
			t.Errorf("got episode %d, want %d", next.Episode, want)
		}
		if err := s.FinishQueued(ctx, next.AnimeID, next.EpKey, nil); err != nil {
			t.Fatal(err)
		}
	}

	if _, ok, _ := s.NextQueued(ctx); ok {
		t.Error("queue should be empty")
	}
}

// A failure is kept and reported rather than disappearing, which would be
// indistinguishable from having worked.
func TestFailedQueueEntryIsKept(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedCatalogue(t, s, 200, "FINISHED", 3)

	if _, err := s.Enqueue(ctx, 200, 1, []int{1}); err != nil {
		t.Fatal(err)
	}
	next, _, err := s.NextQueued(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishQueued(ctx, next.AnimeID, next.EpKey, errors.New("no seeders")); err != nil {
		t.Fatal(err)
	}

	items, err := s.QueuedDownloads(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].State != "failed" {
		t.Fatalf("got %+v, want one failed entry", items)
	}
	if items[0].Error == nil || *items[0].Error != "no seeders" {
		t.Error("the reason should survive")
	}
}

// Cancelling a show clears the whole show, including what is downloading:
// a cancel that leaves an episode still downloading is not a cancel.
func TestDequeueClearsTheShow(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedCatalogue(t, s, 300, "FINISHED", 12)

	if _, err := s.Enqueue(ctx, 300, 1, []int{1, 2, 3, 4}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.NextQueued(ctx); err != nil {
		t.Fatal(err)
	}

	removed, err := s.Dequeue(ctx, 300, "")
	if err != nil {
		t.Fatal(err)
	}
	if removed != 4 {
		t.Errorf("dropped %d, want all 4", removed)
	}

	items, err := s.QueuedDownloads(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("got %+v, want an empty queue", items)
	}
}

// A single episode can be cancelled without touching the rest of the show.
func TestDequeueOneEpisode(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedCatalogue(t, s, 350, "FINISHED", 12)

	if _, err := s.Enqueue(ctx, 350, 1, []int{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	removed, err := s.Dequeue(ctx, 350, "2")
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("dropped %d, want 1", removed)
	}

	items, _ := s.QueuedDownloads(ctx)
	for _, q := range items {
		if q.Episode == 2 {
			t.Error("episode 2 should be gone")
		}
	}
	if len(items) != 2 {
		t.Errorf("%d left, want 2", len(items))
	}
}

// Anything mid-flight when the app stopped has to be picked up again.
func TestResetActiveRequeues(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedCatalogue(t, s, 400, "FINISHED", 3)

	if _, err := s.Enqueue(ctx, 400, 1, []int{1}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.NextQueued(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.ResetActive(ctx); err != nil {
		t.Fatal(err)
	}

	if _, ok, err := s.NextQueued(ctx); err != nil || !ok {
		t.Error("an interrupted download should return to the queue")
	}
}

// A failed entry is the one most likely to be dismissed by hand, so the remove
// button has to work on it directly, not only after a retry.
func TestDequeueRemovesAFailedEntry(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedCatalogue(t, s, 500, "FINISHED", 12)

	if _, err := s.Enqueue(ctx, 500, 1, []int{1}); err != nil {
		t.Fatal(err)
	}
	next, _, err := s.NextQueued(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishQueued(ctx, next.AnimeID, next.EpKey, errors.New("no seeders")); err != nil {
		t.Fatal(err)
	}

	removed, err := s.Dequeue(ctx, 500, next.EpKey)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed %d, want 1", removed)
	}

	items, err := s.QueuedDownloads(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Errorf("got %+v, want the failed entry gone", items)
	}
}
