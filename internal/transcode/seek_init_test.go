package transcode

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

func openClip(t *testing.T, seconds int, id string) (*Session, context.Context) {
	t.Helper()
	ffmpeg, ffprobe := binaries(t)
	clip := testMKV(t, ffmpeg, seconds)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := NewManager(ffmpeg, ffprobe, t.TempDir(), "libx264", log)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	s, err := m.Open(ctx, id, clip)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close(id) })
	return s, ctx
}

func waitExit(s *Session) {
	deadline := time.Now().Add(30 * time.Second)
	for s.running() && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
}

// The reopen that broke in the browser: prestart aims the encoder at the resume
// segment, then the player asks for segment 0 and the init segment together.
// Every ordering of those three must serve all of them; the init request in
// particular must never pull the encoder back toward the resume segment and
// kill the pass serving segment 0.
func TestReopenRequestsInEveryOrderAreServed(t *testing.T) {
	const resume = 5
	orders := [][]string{
		{"prestart", "init", "zero"},
		{"prestart", "zero", "init"},
		{"zero", "init", "prestart"},
		{"init", "zero", "prestart"},
	}
	for i, order := range orders {
		s, ctx := openClip(t, 60, "re"+string(rune('a'+i)))

		var wg sync.WaitGroup
		errs := make(chan error, 3)
		for _, step := range order {
			wg.Add(1)
			go func(step string) {
				defer wg.Done()
				var err error
				switch step {
				case "prestart":
					err = s.Prestart(resume)
				case "init":
					_, err = s.WaitInit(ctx, resume, 60*time.Second)
				case "zero":
					_, err = s.WaitSegment(ctx, 0, 60*time.Second)
				}
				if err != nil {
					errs <- err
				}
			}(step)
			time.Sleep(30 * time.Millisecond)
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Errorf("order %v: %v (produced: %v)", order, err, s.produced())
		}
		// The resume segment is still reachable afterwards.
		if _, err := s.WaitSegment(ctx, resume, 60*time.Second); err != nil {
			t.Errorf("order %v: resume segment after reopen: %v", order, err)
		}
	}
}

// hls.js asks for its timestamp anchor (segment 0) and the resume fragment
// together; the requests pull the encoder opposite ways. Both must be served:
// the restart lets the pass serve the waiter it is about to reach first.
func TestOpposingSegmentRequestsAreBothServed(t *testing.T) {
	s, ctx := openClip(t, 60, "oppose")

	const resume = 6
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, e := s.WaitSegment(ctx, resume, 90*time.Second)
		if e != nil {
			errs <- e
		}
	}()
	time.Sleep(50 * time.Millisecond)
	go func() {
		defer wg.Done()
		_, e := s.WaitSegment(ctx, 0, 90*time.Second)
		if e != nil {
			errs <- e
		}
	}()
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Errorf("%v (produced: %v)", e, s.produced())
	}
}

// WaitInit serves the init file of whatever pass is running and never moves it.
func TestWaitInitNeverRedirectsARunningPass(t *testing.T) {
	s, ctx := openClip(t, 60, "init")

	if err := s.ensureHead(0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.WaitInit(ctx, 8, 60*time.Second); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	from := s.headFrom
	s.mu.Unlock()
	if from != 0 {
		t.Errorf("WaitInit moved the encoder to %d; it must only wait", from)
	}
}

// Prestart is a head start, not a driver: it launches only when nothing runs.
func TestPrestartOnlyWhenIdle(t *testing.T) {
	s, ctx := openClip(t, 60, "pre")

	if err := s.Prestart(5); err != nil {
		t.Fatal(err)
	}
	if !s.running() {
		t.Fatal("prestart on an idle session should launch")
	}
	if err := s.Prestart(0); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	from := s.headFrom
	s.mu.Unlock()
	if from != 5 {
		t.Errorf("prestart moved a running pass to %d", from)
	}
	if _, err := s.WaitSegment(ctx, 5, 60*time.Second); err != nil {
		t.Fatal(err)
	}
}

// A backward request after the pass has run to the end and exited, and one
// while it is still running, both relaunch and deliver.
func TestBackwardRequestsDeliver(t *testing.T) {
	s, ctx := openClip(t, 60, "back")

	if _, err := s.WaitSegment(ctx, 5, 60*time.Second); err != nil {
		t.Fatal(err)
	}
	waitExit(s)
	if _, err := s.WaitSegment(ctx, 0, 60*time.Second); err != nil {
		t.Fatalf("segment 0 after an exited pass: %v (produced: %v)", err, s.produced())
	}

	if err := s.ensureHead(s.Segments - 2); err != nil {
		t.Fatal(err)
	}
	if _, err := s.WaitSegment(ctx, 2, 60*time.Second); err != nil {
		t.Fatalf("segment 2 while a later pass runs: %v (produced: %v)", err, s.produced())
	}
}
