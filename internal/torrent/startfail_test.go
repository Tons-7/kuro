package torrent

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

// A port another program holds must not loop kuro forever: the engine gets a
// free one instead. A free port is kept as configured.
func TestFreeLoopbackMovesOffABusyPort(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	busy := l.Addr().String()

	got, moved := freeLoopback(busy)
	if !moved || got == busy || !strings.HasPrefix(got, "127.0.0.1:") {
		t.Fatalf("busy %s -> %s (moved %v)", busy, got, moved)
	}
	if again, moved := freeLoopback(got); moved || again != got {
		t.Errorf("a free port was moved: %s -> %s", got, again)
	}
}

// An engine that dies at once is reported at once, with what it said, rather
// than after the full start timeout.
func TestWaitReadyStopsWhenTheProcessExits(t *testing.T) {
	exited := make(chan struct{})
	close(exited)
	start := time.Now()
	err := waitReady(context.Background(), NewClient("http://127.0.0.1:1"), 20*time.Second, exited)
	if err == nil || !strings.Contains(err.Error(), "exited") {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Error("waited for the timeout instead of noticing the exit")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Error("reported as a timeout")
	}
}

func TestTailKeepsTheEnd(t *testing.T) {
	var out tail
	out.Write([]byte(strings.Repeat("a", tailBytes)))
	out.Write([]byte("error: address in use\n"))
	got := out.String()
	if !strings.HasSuffix(got, "address in use") || len(got) > tailBytes {
		t.Errorf("tail = %q", got[max(0, len(got)-40):])
	}
}
