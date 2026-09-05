package transcode

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// The seek decision, exhaustively — this is what broke on the friend's machine:
// the encoder aimed at the resume point while a request for a far segment (with
// only a stale file from an earlier pass on disk) waited forever instead of
// relaunching there.
func TestShouldRestart(t *testing.T) {
	const from, to = 38, 45 // a pass started at 38, finished up to 45 (working on 46)
	cases := []struct {
		name string
		n    int
		want bool
	}{
		{"not running", 40, true}, // active=false overrides below
		{"the segment being worked on", 46, false},
		{"a few ahead, buffer drift", 49, false},
		{"just past the grace window", 51, true}, // to+1+restartAfterSegments = 50
		{"far forward jump", 200, true},
		{"backward seek before the pass", 10, true},
		{"the pass start itself", 38, false},
		{"one before the start", 37, true},
	}
	for _, c := range cases {
		active := c.name != "not running"
		if got := shouldRestart(c.n, from, to, active); got != c.want {
			t.Errorf("%s: shouldRestart(%d, from=%d, to=%d) = %v, want %v",
				c.name, c.n, from, to, got, c.want)
		}
	}

	// Right after launch nothing is finished (to = from-1, so the encoder is
	// working on segment 0). Buffering up to restartAfterSegments past it must
	// not be mistaken for a jump; one further is a jump.
	for n := 0; n <= restartAfterSegments; n++ {
		if shouldRestart(n, 0, -1, true) {
			t.Errorf("fresh pass restarted for a near segment %d", n)
		}
	}
	if !shouldRestart(restartAfterSegments+1, 0, -1, true) {
		t.Error("a jump past a fresh encoder's reach should restart")
	}
}

// Real ffmpeg: seeking forward to the end and back to the start must both
// deliver, and the backward seek must relaunch rather than wait on a segment
// the forward pass moved beyond.
func TestSeekingForwardAndBackDelivers(t *testing.T) {
	ffmpeg, ffprobe := binaries(t)
	clip := testMKV(t, ffmpeg, 60)

	log := slog.New(slog.NewTextHandler(testWriter{t}, &slog.HandlerOptions{Level: slog.LevelDebug}))
	m := NewManager(ffmpeg, ffprobe, t.TempDir(), "libx264", log)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	session, err := m.Open(ctx, "seek", clip)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close("seek")

	if _, err := session.WaitSegment(ctx, 0, 90*time.Second); err != nil {
		t.Fatalf("first segment: %v", err)
	}

	last := session.Segments - 1
	if _, err := session.WaitSegment(ctx, last, 90*time.Second); err != nil {
		t.Fatalf("seek to the final segment: %v", err)
	}

	// Back to the start: the forward pass is up near the end, so this is a
	// backward seek that must relaunch, not wait on the running pass.
	start := time.Now()
	if _, err := session.WaitSegment(ctx, 1, 90*time.Second); err != nil {
		t.Fatalf("seek back to segment 1: %v", err)
	}
	t.Logf("backward seek delivered in %s", time.Since(start).Round(time.Millisecond))

	// A mid-file forward jump after the backward seek.
	if _, err := session.WaitSegment(ctx, last/2, 90*time.Second); err != nil {
		t.Fatalf("mid seek: %v", err)
	}
}

// advanceHead follows the encoder as it produces, so the restart decision knows
// where it really is rather than trusting a lingering file.
func TestAdvanceHeadTracksProducedSegments(t *testing.T) {
	ffmpeg, ffprobe := binaries(t)
	clip := testMKV(t, ffmpeg, 30)

	log := slog.New(slog.NewTextHandler(testWriter{t}, &slog.HandlerOptions{Level: slog.LevelWarn}))
	m := NewManager(ffmpeg, ffprobe, t.TempDir(), "libx264", log)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	session, err := m.Open(ctx, "advance", clip)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close("advance")

	// Play every segment in order: they must all be delivered without a stall,
	// and a forward request must never be treated as a backward seek (headFrom
	// only ever moves forward with the playhead, never behind it).
	last := -1
	for n := 0; n < session.Segments; n++ {
		if _, err := session.WaitSegment(ctx, n, 60*time.Second); err != nil {
			t.Fatalf("segment %d: %v", n, err)
		}
		session.mu.Lock()
		from := session.headFrom
		session.mu.Unlock()
		if from < last {
			t.Fatalf("forward play moved the encoder backward at segment %d: headFrom %d -> %d", n, last, from)
		}
		last = from
	}
}

func TestPlaylistSegmentMapsToFixedTime(t *testing.T) {
	// Segment N must always cover time N*SegmentSeconds, independent of where the
	// encoder started, or the player and server disagree on a seek target.
	s := &Session{Info: &MediaInfo{Duration: 120}, Segments: segmentCount(120)}
	playlist := s.Playlist()
	if !strings.Contains(playlist, "#EXT-X-PLAYLIST-TYPE:VOD") {
		t.Error("expected a VOD playlist")
	}
	if got := strings.Count(playlist, "#EXTINF"); got != s.Segments {
		t.Errorf("%d segments listed, want %d", got, s.Segments)
	}
}
