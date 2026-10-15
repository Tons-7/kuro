package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A deferred episode waits its time out behind the rest, then is taken up again with its tries counted.
func TestDeferredEpisodeWaitsForItsTime(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedCatalogue(t, s, 100, "FINISHED", 12)
	if _, err := s.Enqueue(ctx, 100, 1, []int{1, 2}); err != nil {
		t.Fatal(err)
	}
	if next, _, _ := s.NextQueued(ctx); next.Episode != 1 {
		t.Fatalf("started episode %d, want 1", next.Episode)
	}

	if err := s.Defer(ctx, 100, "1", errors.New("the seeders never answered"), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if next, _, _ := s.NextQueued(ctx); next.Episode != 2 {
		t.Errorf("started episode %d while 1 waits for its time, want 2", next.Episode)
	}
	if _, ok, _ := s.PeekQueued(ctx); ok {
		t.Error("the deferred episode is peeked at as if it were due")
	}
	if at, ok, err := s.NextRetry(ctx); err != nil || !ok || time.Until(at) < 50*time.Minute {
		t.Errorf("next retry = %v %v %v, want about an hour away", at, ok, err)
	}

	rows, err := s.QueuedDownloads(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var deferred QueuedDownload
	for _, q := range rows {
		if q.EpKey == "1" {
			deferred = q
		}
	}
	if deferred.State != "pending" || deferred.Attempts != 1 || deferred.RetryAt == 0 ||
		deferred.Error == nil || *deferred.Error != "the seeders never answered" {
		t.Errorf("deferred row = %+v, want pending, one try, a retry time and the reason", deferred)
	}

	// Due now: taken up again, the count carried along.
	if err := s.Defer(ctx, 100, "2", errors.New("stalled"), time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if next, ok, _ := s.NextQueued(ctx); !ok || next.Episode != 2 || next.Attempts != 1 {
		t.Errorf("next = %+v %v, want episode 2 on its second try", next, ok)
	}
	if _, ok, _ := s.NextRetry(ctx); !ok {
		t.Error("episode 1 still waits an hour, yet no retry time is reported")
	}
}

// Asking for the episode again, or moving it to the front, is a wish to try now, not later.
func TestAskingAgainEndsTheWait(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedCatalogue(t, s, 100, "FINISHED", 12)
	later := time.Now().Add(time.Hour)
	for ep := 1; ep <= 3; ep++ {
		if _, err := s.Enqueue(ctx, 100, 1, []int{ep}); err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.NextQueued(ctx); err != nil {
			t.Fatal(err)
		}
		if err := s.Defer(ctx, 100, epKeyOf(ep), errors.New("stalled"), later); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok, _ := s.NextQueued(ctx); ok {
		t.Fatal("an episode started before its time")
	}

	if _, err := s.Enqueue(ctx, 100, 1, []int{1}); err != nil {
		t.Fatal(err)
	}
	if next, ok, _ := s.NextQueued(ctx); !ok || next.Episode != 1 || next.Attempts != 0 {
		t.Errorf("after download pressed again: next = %+v %v, want episode 1 with its tries reset", next, ok)
	}
	if moved, err := s.Prioritise(ctx, 100, "2"); err != nil || !moved {
		t.Fatalf("moved=%v err=%v", moved, err)
	}
	if next, ok, _ := s.NextQueued(ctx); !ok || next.Episode != 2 {
		t.Errorf("after download now: next = %+v %v, want episode 2", next, ok)
	}
	if _, err := s.SetQueuedRelease(ctx, 100, "3", "abc", "[Other] Show - 03"); err != nil {
		t.Fatal(err)
	}
	if next, ok, _ := s.NextQueued(ctx); !ok || next.Episode != 3 || next.Attempts != 0 {
		t.Errorf("after a release was chosen: next = %+v %v, want episode 3 with its tries reset", next, ok)
	}
}

func epKeyOf(ep int) string { return string(rune('0' + ep)) }
