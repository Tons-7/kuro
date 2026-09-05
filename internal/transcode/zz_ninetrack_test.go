package transcode

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"
)

// The 9-subtitle-track HEVC file that dies instantly inside a live server.
func TestNineTrackFileEncodes(t *testing.T) {
	src := os.Getenv("NINETRACK")
	if src == "" {
		t.Skip("set NINETRACK")
	}
	ffmpeg, ffprobe := binaries(t)
	log := slog.New(slog.NewTextHandler(testWriter{t}, &slog.HandlerOptions{Level: slog.LevelDebug}))
	m := NewManager(ffmpeg, ffprobe, t.TempDir(), "h264_nvenc", log)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	s, err := m.Open(ctx, "nine", src)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close("nine")
	t.Logf("plan: %+v", s.Plan)

	// The server's exact concurrency: prestart, init and segment 0 together.
	var wg sync.WaitGroup
	wg.Add(3)
	var initErr, segErr error
	go func() { defer wg.Done(); _ = s.Prestart(0) }()
	go func() { defer wg.Done(); _, initErr = s.WaitInit(ctx, 0, 60*time.Second) }()
	go func() { defer wg.Done(); _, segErr = s.WaitSegment(ctx, 0, 60*time.Second) }()
	wg.Wait()
	if initErr != nil || segErr != nil {
		t.Fatalf("init=%v seg=%v (produced: %v)", initErr, segErr, s.produced())
	}
}
