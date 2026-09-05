package transcode

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"
)

// A hand-picked release reopens the same episode from another source. The old
// encoder must go, and the new session must own the directory and be the one a
// later Close releases.
func TestReopenOnAnotherSourceReplacesTheSession(t *testing.T) {
	ffmpeg, ffprobe := binaries(t)
	first := testMKV(t, ffmpeg, 20)
	second := testMKV(t, ffmpeg, 20)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := NewManager(ffmpeg, ffprobe, t.TempDir(), "libx264", log)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	old, err := m.Open(ctx, "1-1", first)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.WaitSegment(ctx, 0, 60*time.Second); err != nil {
		t.Fatal(err)
	}

	fresh, err := m.Open(ctx, "1-1", second)
	if err != nil {
		t.Fatal(err)
	}
	if fresh == old {
		t.Fatal("a different source returned the old session")
	}
	if old.running() {
		t.Fatal("the replaced session's encoder is still running")
	}
	if got, _ := m.Get("1-1"); got != fresh {
		t.Fatal("the manager does not hold the new session")
	}
	if _, err := fresh.WaitSegment(ctx, 0, 60*time.Second); err != nil {
		t.Fatalf("new session cannot produce: %v", err)
	}
	if _, err := os.Stat(fresh.dir); err != nil {
		t.Fatal("the shared directory was removed from under the new session")
	}

	if !m.Close("1-1") {
		t.Fatal("closing the sole holder did not release the session")
	}
	if _, ok := m.Get("1-1"); ok {
		t.Fatal("session survives its close")
	}
}
