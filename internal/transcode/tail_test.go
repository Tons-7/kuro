package transcode

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSegmentCountFoldsSlivers(t *testing.T) {
	cases := map[float64]int{
		180.021: 30, // a 21ms sliver folds into segment 29
		180.0:   30,
		181.0:   31, // a whole second stands on its own
		1435.0:  240,
		5.0:     1,
		0.0:     1,
	}
	for duration, want := range cases {
		if got := segmentCount(duration); got != want {
			t.Errorf("segmentCount(%v) = %d, want %d", duration, got, want)
		}
	}

	s := &Session{Info: &MediaInfo{Duration: 180.021}, Segments: segmentCount(180.021)}
	playlist := s.Playlist()
	if strings.Contains(playlist, "30.mp4") {
		t.Error("playlist advertises a sliver segment nothing will write")
	}
	if !strings.Contains(playlist, "#EXTINF:6.021,\n29.mp4") {
		t.Errorf("the last segment should carry the sliver:\n%s", playlist)
	}
}

// Stream copy cuts on the file's keyframes: with a long GOP the last keyframe
// can fall short of the arithmetic, so a pass from the start stops before the
// final segment and the player waits for it forever.
func TestTrailingSegmentIsWrittenWhenThePassEndsShort(t *testing.T) {
	ffmpeg, ffprobe := binaries(t)

	clip := filepath.Join(t.TempDir(), "longgop.mkv")
	cmd := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=320x180:rate=24:duration=25.5",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=25.5",
		"-c:v", "libx264", "-preset", "ultrafast", "-g", "168", "-keyint_min", "168",
		"-sc_threshold", "0", "-pix_fmt", "yuv420p", "-c:a", "aac", clip)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("encode clip: %v: %s", err, out)
	}

	m := NewManager(ffmpeg, ffprobe, t.TempDir(), softwareEncoder, slog.Default())
	ctx := context.Background()
	s, err := m.Open(ctx, "7-1", clip)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close("7-1") })
	if !s.Plan.VideoCopy {
		t.Fatalf("the clip should be remuxed, not re-encoded: %+v", s.Plan)
	}
	if s.Segments != 5 {
		t.Fatalf("segments = %d, want 5", s.Segments)
	}

	// Play through in order, as a player does; the first pass runs out of
	// keyframes before segment 4.
	for n := 0; n < s.Segments; n++ {
		if _, err := s.WaitSegment(ctx, n, 60*time.Second); err != nil {
			t.Fatalf("segment %d: %v", n, err)
		}
	}

	// And it holds the tail of the file, not nothing.
	probe := exec.Command(ffprobe, "-v", "error", "-show_entries", "format=duration",
		"-of", "csv=p=0", "concat:"+s.InitPath()+"|"+s.SegmentPath(4))
	out, err := probe.Output()
	if err != nil {
		t.Fatalf("probe segment 4: %v", err)
	}
	var seconds float64
	fmt.Sscanf(strings.TrimSpace(string(out)), "%f", &seconds)
	if seconds < 1 {
		t.Errorf("segment 4 holds %.2fs; expected the file's last seconds", seconds)
	}
}
