package config

import (
	"context"
	"os/exec"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

func stubWindow(t *testing.T, window func(context.Context, string) *launched) *atomic.Int32 {
	t.Helper()
	var tabs atomic.Int32

	oldWindow, oldTab := showWindow, showTab
	showWindow = window
	showTab = func(string) { tabs.Add(1) }
	t.Cleanup(func() { showWindow, showTab = oldWindow, oldTab })

	return &tabs
}

func TestOpenAppStopsWhenTheWindowStaysUp(t *testing.T) {
	var attempts atomic.Int32
	tabs := stubWindow(t, func(context.Context, string) *launched {
		attempts.Add(1)
		return &launched{}
	})

	OpenApp(context.Background(), "http://localhost:4321")

	if attempts.Load() != 1 {
		t.Errorf("tried %d times, want one", attempts.Load())
	}
	if tabs.Load() != 0 {
		t.Error("opened a browser tab as well as the window")
	}
}

// After an update the previous window is still releasing the profile, and the
// browser that cannot take it exits at once showing nothing.
func TestOpenAppRetriesOnceBeforeGivingUp(t *testing.T) {
	var attempts atomic.Int32
	tabs := stubWindow(t, func(context.Context, string) *launched {
		if attempts.Add(1) > 1 {
			return &launched{}
		}
		return nil
	})

	OpenApp(context.Background(), "http://localhost:4321")

	if attempts.Load() != 2 {
		t.Errorf("tried %d times, want a second attempt", attempts.Load())
	}
	if tabs.Load() != 0 {
		t.Error("fell back to a tab even though the retry worked")
	}
}

// The old behaviour: a browser that started counted as success, so a failure to
// show anything left the app with no interface at all.
func TestOpenAppFallsBackToATab(t *testing.T) {
	var attempts atomic.Int32
	tabs := stubWindow(t, func(context.Context, string) *launched {
		attempts.Add(1)
		return nil
	})

	OpenApp(context.Background(), "http://localhost:4321")

	if attempts.Load() != 2 {
		t.Errorf("tried %d times, want both attempts", attempts.Load())
	}
	if tabs.Load() != 1 {
		t.Errorf("opened %d tabs, want exactly one fallback", tabs.Load())
	}
}

func TestOpenAppGivesUpWhenCancelled(t *testing.T) {
	var attempts atomic.Int32
	tabs := stubWindow(t, func(context.Context, string) *launched {
		attempts.Add(1)
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	OpenApp(ctx, "http://localhost:4321")

	if attempts.Load() != 0 || tabs.Load() != 0 {
		t.Errorf("attempts=%d tabs=%d, want nothing opened after cancellation",
			attempts.Load(), tabs.Load())
	}
}

// Quitting during the wait between attempts must not open anything.
func TestOpenAppRetryHonoursCancellation(t *testing.T) {
	var attempts atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	tabs := stubWindow(t, func(context.Context, string) *launched {
		if attempts.Add(1) == 1 {
			cancel()
		}
		return nil
	})

	done := make(chan struct{})
	go func() {
		OpenApp(ctx, "http://localhost:4321")
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("OpenApp did not return")
	}
	if attempts.Load() != 1 {
		t.Errorf("tried %d times after cancellation, want one", attempts.Load())
	}
	if tabs.Load() != 0 {
		t.Errorf("opened %d tabs while shutting down, want none", tabs.Load())
	}
}

// The window goes with kuro, so the profile is free for whatever starts next.
func TestCloseEndsTheWindowProcess(t *testing.T) {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/c", "ping -n 30 127.0.0.1 >NUL")
	} else {
		cmd = exec.Command("sleep", "30")
	}
	if err := cmd.Start(); err != nil {
		t.Skip("no long-running command available:", err)
	}
	exited := make(chan struct{})
	go func() {
		cmd.Wait()
		close(exited)
	}()

	stubWindow(t, func(context.Context, string) *launched {
		return &launched{cmd: cmd, exited: exited}
	})
	w := &Window{}
	w.Open(context.Background(), "http://localhost:4321")

	start := time.Now()
	w.Close()
	select {
	case <-exited:
	default:
		t.Fatal("the window process is still running after Close")
	}
	if time.Since(start) > 3*time.Second {
		t.Error("Close waited on the timeout rather than the process")
	}

	// Closing again, or a window that was never opened, is nothing.
	w.Close()
	(&Window{}).Close()
}

func TestCloseHasNothingToDoAfterAHandOff(t *testing.T) {
	stubWindow(t, func(context.Context, string) *launched { return &launched{} })
	w := &Window{}
	w.Open(context.Background(), "http://localhost:4321")
	w.Close()
}
