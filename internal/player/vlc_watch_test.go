package player

import (
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"slices"
	"sync"
	"testing"
	"time"
)

// Stands in for the VLC process: the test binary re-run, doing nothing until killed.
func TestVLCStandIn(t *testing.T) {
	if os.Getenv("KURO_VLC_STAND_IN") != "1" {
		t.Skip("only as the stand-in process")
	}
	time.Sleep(time.Minute)
}

// fakeVLC is VLC's control interface answering one scripted status per poll, the last one for ever.
type fakeVLC struct {
	mu     sync.Mutex
	script []vlcStatus
	seeks  []string
	noAuth bool
}

func (f *fakeVLC) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, pw, ok := r.BasicAuth(); !ok || pw != "pw" {
		f.noAuth = true
		http.Error(w, "no", http.StatusUnauthorized)
		return
	}
	if r.URL.Query().Get("command") == "seek" {
		f.seeks = append(f.seeks, r.URL.Query().Get("val"))
		io.WriteString(w, "{}")
		return
	}
	st := f.script[0]
	if len(f.script) > 1 {
		f.script = f.script[1:]
	}
	json.NewEncoder(w).Encode(st)
}

// watched runs the tracking loop against the script and returns every event until the channel closes.
func watched(t *testing.T, opts Options, script ...vlcStatus) ([]Event, *fakeVLC, *exec.Cmd) {
	t.Helper()
	fake := &fakeVLC{script: script}
	srv := httptest.NewServer(http.HandlerFunc(fake.handler))
	t.Cleanup(srv.Close)

	cmd := exec.Command(os.Args[0], "-test.run=^TestVLCStandIn$")
	cmd.Env = append(os.Environ(), "KURO_VLC_STAND_IN=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill() })

	v := NewVLC("", slog.New(slog.NewTextHandler(io.Discard, nil)))
	events := make(chan Event, 64)
	v.cmd = cmd
	go v.watch(cmd, control{port: srv.Listener.Addr().(*net.TCPAddr).Port, password: "pw"}, opts, events)

	var got []Event
	deadline := time.After(20 * time.Second)
	for {
		select {
		case e, open := <-events:
			if !open {
				return got, fake, cmd
			}
			got = append(got, e)
		case <-deadline:
			t.Fatalf("VLC was never let go; events so far: %+v", got)
		}
	}
}

func last(events []Event, kind EventKind) (Event, bool) {
	for _, e := range slices.Backward(events) {
		if e.Kind == kind {
			return e, true
		}
	}
	return Event{}, false
}

// Stopping within the last seconds is the episode ending; VLC is closed so the next one can open.
func TestVLCReachingTheEndIsReportedAsTheEnd(t *testing.T) {
	events, fake, _ := watched(t, Options{},
		vlcStatus{State: "playing", Time: 10, Length: 100},
		vlcStatus{State: "paused", Time: 11, Length: 100},
		// Position is a fraction of the length and more exact than the whole-second time.
		vlcStatus{State: "playing", Time: 98, Length: 100, Position: 0.985},
		vlcStatus{State: "stopped"},
	)

	end, ok := last(events, EventEnd)
	if !ok || end.Reason != "eof" || end.Position != 98.5 || end.Duration != 100 {
		t.Fatalf("end = %+v (found %v), want eof at 98.5 of 100", end, ok)
	}
	if _, exited := last(events, EventExit); exited {
		t.Error("the end was reported a second time as the player being closed")
	}
	var pauses []bool
	for _, e := range events {
		if e.Kind == EventPause {
			pauses = append(pauses, e.Paused)
		}
	}
	if len(pauses) != 2 || !pauses[0] || pauses[1] {
		t.Errorf("pause events = %v, want paused then resumed", pauses)
	}
	if fake.noAuth {
		t.Error("a request went out without the interface password")
	}
}

// Stopped part-way is the viewer's own stop: the episode is not finished.
func TestVLCStoppedPartWayIsNotTheEnd(t *testing.T) {
	events, _, _ := watched(t, Options{},
		vlcStatus{State: "stopped"}, // not started yet: VLC reports stopped while it opens the stream
		vlcStatus{State: "playing", Time: 40, Length: 100},
		vlcStatus{State: "stopped"},
	)
	end, ok := last(events, EventEnd)
	if !ok || end.Reason != "stop" || end.Position != 40 {
		t.Fatalf("end = %+v (found %v), want a stop at 40", end, ok)
	}
}

func TestVLCClosedByTheViewerReportsWhereItWas(t *testing.T) {
	fake := &fakeVLC{script: []vlcStatus{{State: "playing", Time: 25, Length: 100}}}
	srv := httptest.NewServer(http.HandlerFunc(fake.handler))
	t.Cleanup(srv.Close)

	cmd := exec.Command(os.Args[0], "-test.run=^TestVLCStandIn$")
	cmd.Env = append(os.Environ(), "KURO_VLC_STAND_IN=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	v := NewVLC("", slog.New(slog.NewTextHandler(io.Discard, nil)))
	events := make(chan Event, 64)
	v.cmd = cmd
	go v.watch(cmd, control{port: srv.Listener.Addr().(*net.TCPAddr).Port, password: "pw"}, Options{}, events)

	var exit Event
	for e := range events {
		if e.Kind == EventPosition {
			cmd.Process.Kill()
		}
		exit = e
	}
	if exit.Kind != EventExit || exit.Position != 25 || exit.Duration != 100 {
		t.Fatalf("last event = %+v, want an exit at 25 of 100", exit)
	}
	if v.Running() {
		t.Error("VLC is still counted as running after it was closed")
	}
}

// Inside the opening with auto-skip on, VLC is told to jump to its end, once.
func TestVLCAutoSkipSeeksPastTheOpeningOnce(t *testing.T) {
	opts := Options{AutoSkip: true, SkipRanges: []SkipRange{{Kind: "op", Start: 10, End: 40}}}
	_, fake, _ := watched(t, opts,
		vlcStatus{State: "playing", Time: 5, Length: 100},
		vlcStatus{State: "playing", Time: 12, Length: 100},
		// Still inside on the next poll: the seek has not landed yet.
		vlcStatus{State: "playing", Time: 13, Length: 100},
		vlcStatus{State: "playing", Time: 41, Length: 100},
		vlcStatus{State: "stopped"},
	)
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.seeks) != 1 || fake.seeks[0] != "40" {
		t.Fatalf("seeks = %v, want one to 40", fake.seeks)
	}
}
