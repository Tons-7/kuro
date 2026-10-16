package transcode

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// stalledSource answers every read with headers and then nothing, as a file whose start has not arrived.
func stalledSource(t *testing.T) (url string, opened *atomic.Int32) {
	t.Helper()
	opened = &atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		opened.Add(1)
		w.Header().Set("Content-Type", "video/x-matroska")
		w.Header().Set("Content-Length", "1000000")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/file.mkv", opened
}

func shortProbes(t *testing.T) {
	t.Helper()
	was := probeLimit
	probeLimit = 700 * time.Millisecond
	t.Cleanup(func() { probeLimit = was })
}

// A slow download buffers: the wait for the file's start lasts while bytes keep arriving.
func TestProbeWaitsOnWhileTheDownloadMoves(t *testing.T) {
	ffmpeg, ffprobe := binaries(t)
	shortProbes(t)
	source, opened := stalledSource(t)
	m := NewManager(ffmpeg, ffprobe, t.TempDir(), "libx264", slog.New(slog.NewTextHandler(io.Discard, nil)))

	// Arriving, arriving, then stopped.
	arrived := []int64{0, 4 << 20, 9 << 20, 9 << 20, 9 << 20}
	var asked int
	m.OnArriving(func(context.Context, string) int64 {
		v := arrived[min(asked, len(arrived)-1)]
		asked++
		return v
	})

	_, err := m.probeWhileArriving(context.Background(), "1-1", source)
	if !errors.Is(err, ErrProbeTimeout) {
		t.Fatalf("err = %v, want the timeout once the download stopped", err)
	}
	if got := opened.Load(); got != 3 {
		t.Errorf("read the source %d times, want 3: two extensions while it moved, then the end", got)
	}
}

// With nothing to say whether the download moves, the wait is one limit, as it was.
func TestProbeWithoutProgressWaitsOnce(t *testing.T) {
	ffmpeg, ffprobe := binaries(t)
	shortProbes(t)
	source, opened := stalledSource(t)
	m := NewManager(ffmpeg, ffprobe, t.TempDir(), "libx264", slog.New(slog.NewTextHandler(io.Discard, nil)))

	if _, err := m.probeWhileArriving(context.Background(), "1-1", source); !errors.Is(err, ErrProbeTimeout) {
		t.Fatalf("err = %v", err)
	}
	if got := opened.Load(); got != 1 {
		t.Errorf("read the source %d times, want once", got)
	}
}

// The wait is not endless: a download that crawls for ever still ends in an answer.
func TestProbeWaitIsCapped(t *testing.T) {
	ffmpeg, ffprobe := binaries(t)
	shortProbes(t)
	source, opened := stalledSource(t)
	m := NewManager(ffmpeg, ffprobe, t.TempDir(), "libx264", slog.New(slog.NewTextHandler(io.Discard, nil)))
	var arrived int64
	m.OnArriving(func(context.Context, string) int64 { arrived += 1 << 20; return arrived })

	if _, err := m.probeWhileArriving(context.Background(), "1-1", source); !errors.Is(err, ErrProbeTimeout) {
		t.Fatalf("err = %v", err)
	}
	if got := int(opened.Load()); got != probeExtensions+1 {
		t.Errorf("read the source %d times, want %d", got, probeExtensions+1)
	}
}
