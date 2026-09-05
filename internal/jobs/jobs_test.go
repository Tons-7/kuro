package jobs

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

func newScheduler() *Scheduler {
	return New(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestRunsOnStartAndRecordsSuccess(t *testing.T) {
	s := newScheduler()
	var runs atomic.Int32

	s.Add(Job{
		Name: "seed", Every: time.Hour, OnStart: true,
		Run: func(context.Context) error { runs.Add(1); return nil },
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)

	waitFor(t, func() bool { return runs.Load() == 1 })

	st := s.Status()[0]
	if st.Runs != 1 || st.Failures != 0 || st.LastErr != "" {
		t.Fatalf("status = %+v", st)
	}
	if st.LastOK.IsZero() {
		t.Error("successful run not recorded")
	}
}

func TestFailureIsRecordedNotFatal(t *testing.T) {
	s := newScheduler()
	s.Add(Job{
		Name: "broken", Every: time.Hour, OnStart: true,
		Run: func(context.Context) error { return errors.New("upstream down") },
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)

	waitFor(t, func() bool { return s.Status()[0].Failures == 1 })

	st := s.Status()[0]
	if st.LastErr != "upstream down" {
		t.Errorf("lastError = %q", st.LastErr)
	}
	if !st.LastOK.IsZero() {
		t.Error("a failure must not count as a success")
	}
}

// One job panicking must not take the process down with it.
func TestPanicIsContained(t *testing.T) {
	s := newScheduler()
	s.Add(Job{
		Name: "panics", Every: time.Hour, OnStart: true,
		Run: func(context.Context) error { panic("boom") },
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)

	waitFor(t, func() bool { return s.Status()[0].Failures == 1 })
	if got := s.Status()[0].LastErr; got != "panic: boom" {
		t.Fatalf("lastError = %q", got)
	}
}

// A slow job must not be started again while it is still running.
func TestNoOverlappingRuns(t *testing.T) {
	s := newScheduler()
	var concurrent, peak atomic.Int32

	release := make(chan struct{})
	s.Add(Job{
		Name: "slow", Every: time.Millisecond, OnStart: true,
		Run: func(context.Context) error {
			n := concurrent.Add(1)
			if n > peak.Load() {
				peak.Store(n)
			}
			<-release
			concurrent.Add(-1)
			return nil
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)

	waitFor(t, func() bool { return s.Status()[0].Running })
	time.Sleep(50 * time.Millisecond)
	close(release)

	if peak.Load() > 1 {
		t.Fatalf("%d concurrent runs of the same job", peak.Load())
	}
}

func TestTriggerRunsImmediately(t *testing.T) {
	s := newScheduler()
	var runs atomic.Int32

	s.Add(Job{
		Name: "manual", Every: time.Hour,
		Run: func(context.Context) error { runs.Add(1); return nil },
	})

	if err := s.Trigger(context.Background(), "manual"); err != nil {
		t.Fatal(err)
	}
	if runs.Load() != 1 {
		t.Fatalf("runs = %d", runs.Load())
	}
	if err := s.Trigger(context.Background(), "missing"); err == nil {
		t.Error("triggering an unknown job should error")
	}
}

// A persistently broken job should stop hammering whatever it depends on.
func TestBackoffGrowsAndIsCapped(t *testing.T) {
	base := time.Minute

	if backoff(base, 1) <= base {
		t.Error("first failure should already back off")
	}
	if backoff(base, 3) <= backoff(base, 1) {
		t.Error("backoff should grow with consecutive failures")
	}
	if got := backoff(time.Hour, 99); got > 6*time.Hour {
		t.Errorf("backoff = %s, want it capped", got)
	}
}

func TestAddIgnoresInvalidJobs(t *testing.T) {
	s := newScheduler()
	s.Add(Job{Name: "", Run: func(context.Context) error { return nil }})
	s.Add(Job{Name: "nofunc"})
	s.Add(Job{Name: "dup", Every: time.Hour, Run: func(context.Context) error { return nil }})
	s.Add(Job{Name: "dup", Every: time.Hour, Run: func(context.Context) error { return nil }})

	if got := len(s.Status()); got != 1 {
		t.Fatalf("registered %d jobs, want 1", got)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
