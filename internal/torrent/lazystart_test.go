package torrent

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// answering stands in for a running rqbit, and can be switched off to stand in
// for one that died.
type answering struct {
	srv  *httptest.Server
	live atomic.Bool
	hits atomic.Int32
}

func newAnswering(t *testing.T) *answering {
	t.Helper()
	a := &answering{}
	a.live.Store(true)
	a.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.hits.Add(1)
		if !a.live.Load() {
			panic(http.ErrAbortHandler)
		}
		w.Write([]byte(`{"torrents":[]}`))
	}))
	t.Cleanup(a.srv.Close)
	return a
}

func supervisorFor(t *testing.T, addr string) *Supervisor {
	t.Helper()
	return NewSupervisor(Options{
		Binary:   filepath.Join(t.TempDir(), "does-not-exist.exe"),
		CacheDir: t.TempDir(),
		APIAddr:  addr,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func addrOf(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	return strings.TrimPrefix(srv.URL, "http://")
}

func TestEnsureReusesARunningEngine(t *testing.T) {
	engine := newAnswering(t)
	s := supervisorFor(t, addrOf(t, engine.srv))

	if err := s.Ensure(context.Background()); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if s.cmd != nil {
		t.Error("spawned an engine while one was already answering")
	}
}

func TestCallsFailClearlyWithoutAnEngine(t *testing.T) {
	s := supervisorFor(t, "127.0.0.1:1")

	_, err := s.Client().List(context.Background())
	if err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("err = %v, want it to say the engine is missing", err)
	}
	if !s.lastTry.IsZero() {
		t.Error("a missing binary armed the throttle; an install would then wait it out")
	}
}

func TestClientRecoversWhenTheEngineAppears(t *testing.T) {
	engine := newAnswering(t)
	engine.live.Store(false)
	s := supervisorFor(t, addrOf(t, engine.srv))
	client := s.Client()

	if _, err := client.List(context.Background()); err == nil {
		t.Fatal("a call against a dead engine should fail")
	}

	engine.live.Store(true)
	if _, err := client.List(context.Background()); err != nil {
		t.Fatalf("the engine answers again but the call failed: %v", err)
	}
}

func TestADeadEngineIsNoticedAndRetried(t *testing.T) {
	engine := newAnswering(t)
	s := supervisorFor(t, addrOf(t, engine.srv))
	client := s.Client()

	if _, err := client.List(context.Background()); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if !s.up {
		t.Fatal("a successful call should leave the engine marked up")
	}

	engine.live.Store(false)
	if _, err := client.List(context.Background()); err == nil {
		t.Fatal("a call to a dead engine should fail")
	}
	if s.up {
		t.Error("the engine is dead but still marked up; later calls would never retry")
	}

	engine.live.Store(true)
	if _, err := client.List(context.Background()); err != nil {
		t.Fatalf("recovered engine: %v", err)
	}
}

// A caller giving up says nothing about the engine; treating it as dead used
// to force a probe under the lock on the next call and, with a dead context,
// a second spawn on top of the live one.
func TestACallersOwnCancellationDoesNotMarkTheEngineDown(t *testing.T) {
	engine := newAnswering(t)
	s := supervisorFor(t, addrOf(t, engine.srv))
	client := s.Client()

	if _, err := client.List(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.List(ctx); err == nil {
		t.Fatal("a cancelled call should fail")
	}
	if !s.up {
		t.Error("the caller's cancellation was taken for a dead engine")
	}
}

func TestEnsureRefusesADeadContextWithoutSpawning(t *testing.T) {
	s := supervisorFor(t, "127.0.0.1:1")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := s.Ensure(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want the context error", err)
	}
	if s.cmd != nil || !s.lastTry.IsZero() {
		t.Error("a dead context caused a spawn attempt")
	}
}

// A spawn that fails is not retried on every request.
func TestFailedSpawnsAreThrottled(t *testing.T) {
	s := supervisorFor(t, "127.0.0.1:1")
	s.opts.Binary = filepath.Join(t.TempDir(), "not-a-program.txt")
	if err := os.WriteFile(s.opts.Binary, []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	if err := s.Ensure(ctx); err == nil {
		t.Fatal("ensure should fail when the binary cannot start")
	}
	armed := s.lastTry
	if armed.IsZero() {
		t.Fatal("a failed spawn should arm the throttle")
	}
	err := s.Ensure(ctx)
	if err == nil || !strings.Contains(err.Error(), "retried shortly") {
		t.Fatalf("err = %v, want the throttled message", err)
	}
	if !s.lastTry.Equal(armed) {
		t.Error("a second attempt was made straight away")
	}
}

// An engine on another machine is used as found; kuro must never try to spawn
// one locally to stand in for it.
func TestRemoteEngineIsNeverSpawned(t *testing.T) {
	for addr, remote := range map[string]bool{
		"127.0.0.1:3030": false, "localhost:3030": false, "[::1]:3030": false,
		"192.168.1.20:3030": true, "10.0.0.5:3030": true,
	} {
		if got := supervisorFor(t, addr).Remote(); got != remote {
			t.Errorf("Remote(%q) = %v, want %v", addr, got, remote)
		}
	}

	s := supervisorFor(t, "192.168.1.20:1")
	err := s.Ensure(context.Background())
	if err == nil || !strings.Contains(err.Error(), "192.168.1.20:1") {
		t.Fatalf("err = %v, want the remote address named", err)
	}
	if s.cmd != nil || !s.lastTry.IsZero() {
		t.Error("a spawn was attempted for a remote engine")
	}
}

func TestStartReturnsTheSharedClient(t *testing.T) {
	engine := newAnswering(t)
	s := supervisorFor(t, addrOf(t, engine.srv))

	client, err := s.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if client != s.Client() {
		t.Error("Start and Client should hand out the same client")
	}
}

func TestEnsureIsSafeUnderConcurrentCalls(t *testing.T) {
	engine := newAnswering(t)
	s := supervisorFor(t, addrOf(t, engine.srv))
	client := s.Client()

	errs := make(chan error, 8)
	for range cap(errs) {
		go func() {
			_, err := client.List(context.Background())
			errs <- err
		}()
	}
	for range cap(errs) {
		if err := <-errs; err != nil {
			t.Errorf("concurrent call failed: %v", err)
		}
	}
}

// Callers arriving during a start wait for it rather than starting another.
func TestCallersWaitForAStartInProgress(t *testing.T) {
	engine := newAnswering(t)
	s := supervisorFor(t, addrOf(t, engine.srv))

	done := make(chan struct{})
	s.mu.Lock()
	s.starting = done
	s.mu.Unlock()

	finished := make(chan error, 1)
	go func() { finished <- s.Ensure(context.Background()) }()

	select {
	case err := <-finished:
		t.Fatalf("returned %v before the start finished", err)
	case <-time.After(100 * time.Millisecond):
	}

	s.mu.Lock()
	s.starting = nil
	s.up = true
	s.mu.Unlock()
	close(done)

	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("after the start: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the waiter never woke up")
	}
}

func TestWaitingCallerHonoursItsOwnContext(t *testing.T) {
	s := supervisorFor(t, "127.0.0.1:1")
	s.mu.Lock()
	s.starting = make(chan struct{})
	s.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := s.Ensure(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the waiter's own deadline", err)
	}
}
