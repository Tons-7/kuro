package player

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// These drive a real mpv over its IPC socket (a named pipe on Windows); that
// seam can't be unit tested and is the part most likely to be wrong.
func binaries(t *testing.T) (mpv, ffmpeg string) {
	t.Helper()

	root := os.Getenv("KURO_BIN")
	if root == "" {
		root = filepath.Join("..", "..", "bin")
	}
	mpv = filepath.Join(root, "mpv"+exeSuffix())
	ffmpeg = filepath.Join(root, "ffmpeg"+exeSuffix())

	for _, p := range []string{mpv, ffmpeg} {
		if _, err := os.Stat(p); err != nil {
			t.Skipf("%s not present; skipping integration test", p)
		}
	}
	return mpv, ffmpeg
}

func exeSuffix() string {
	if os.PathSeparator == '\\' {
		return ".exe"
	}
	return ""
}

// A generated clip keeps the test deterministic and offline.
func testClip(t *testing.T, ffmpeg string, seconds int) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "clip.mkv")
	cmd := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=320x180:rate=24:duration="+itoa(seconds),
		"-f", "lavfi", "-i", "sine=frequency=440:duration="+itoa(seconds),
		"-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac", "-shortest", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate clip: %v\n%s", err, out)
	}
	return path
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func newPlayer(t *testing.T, mpv string) *MPV {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	// A unique socket name keeps parallel runs from colliding.
	p := New(mpv, socketName(t.Name()), log)
	t.Cleanup(p.Stop)
	return p
}

func socketName(test string) string {
	if os.PathSeparator == '\\' {
		return `\\.\pipe\kuro-test-` + test
	}
	return filepath.Join(os.TempDir(), "kuro-test-"+test)
}

func TestMPVReportsPosition(t *testing.T) {
	mpv, ffmpeg := binaries(t)
	clip := testClip(t, ffmpeg, 10)

	p := newPlayer(t, mpv)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := p.Play(ctx, Options{URL: clip, Title: "kuro integration", Extra: []string{"--no-video"}}); err != nil {
		t.Fatalf("start mpv: %v", err)
	}

	var sawPosition, sawEnd bool
	var lastPos, duration float64

	deadline := time.After(45 * time.Second)
	for !sawEnd {
		select {
		case ev := <-p.Events():
			switch ev.Kind {
			case EventPosition:
				sawPosition = true
				lastPos, duration = ev.Position, ev.Duration
			case EventEnd:
				sawEnd = true
			case EventExit:
				sawEnd = true
			}
		case <-deadline:
			t.Fatalf("no end event; position seen=%v last=%.1f", sawPosition, lastPos)
		}
	}

	if !sawPosition {
		t.Fatal("no position events arrived over the IPC socket")
	}
	if duration < 8 || duration > 12 {
		t.Errorf("duration = %.1f, want about 10", duration)
	}
	if lastPos < 5 {
		t.Errorf("last reported position = %.1f, want near the end", lastPos)
	}
}

func TestMPVSeek(t *testing.T) {
	mpv, ffmpeg := binaries(t)
	clip := testClip(t, ffmpeg, 30)

	p := newPlayer(t, mpv)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := p.Play(ctx, Options{URL: clip, Extra: []string{"--no-video", "--pause=yes"}}); err != nil {
		t.Fatal(err)
	}

	// Wait for the first position report so the file is loaded.
	waitPosition(t, p, 15*time.Second)

	if err := p.Seek(20); err != nil {
		t.Fatalf("seek: %v", err)
	}

	deadline := time.After(15 * time.Second)
	for {
		select {
		case ev := <-p.Events():
			if ev.Kind == EventPosition && ev.Position >= 19 {
				return
			}
		case <-deadline:
			t.Fatal("position never reflected the seek")
		}
	}
}

// The whole point of auto-skip: the controller watches position and jumps the
// range without the user touching anything.
func TestMPVAutoSkip(t *testing.T) {
	mpv, ffmpeg := binaries(t)
	clip := testClip(t, ffmpeg, 30)

	p := newPlayer(t, mpv)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	err := p.Play(ctx, Options{
		URL:        clip,
		AutoSkip:   true,
		SkipRanges: []SkipRange{{Kind: "op", Start: 2, End: 18}},
		Extra:      []string{"--no-video"},
	})
	if err != nil {
		t.Fatal(err)
	}

	deadline := time.After(30 * time.Second)
	for {
		select {
		case ev := <-p.Events():
			if ev.Kind == EventPosition && ev.Position >= 18 {
				return
			}
			if ev.Kind == EventEnd || ev.Kind == EventExit {
				t.Fatal("playback ended without the skip taking effect")
			}
		case <-deadline:
			t.Fatal("opening was never skipped")
		}
	}
}

func TestMPVStopIsIdempotent(t *testing.T) {
	mpv, ffmpeg := binaries(t)
	clip := testClip(t, ffmpeg, 10)

	p := newPlayer(t, mpv)
	ctx := context.Background()

	if err := p.Play(ctx, Options{URL: clip, Extra: []string{"--no-video"}}); err != nil {
		t.Fatal(err)
	}
	waitPosition(t, p, 15*time.Second)

	if !p.Running() {
		t.Fatal("player should report running")
	}
	p.Stop()
	p.Stop()

	if p.Running() {
		t.Fatal("player still reports running after stop")
	}
	if err := p.Seek(5); err == nil {
		t.Fatal("commands should fail once stopped")
	}
}

func waitPosition(t *testing.T, p *MPV, within time.Duration) {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case ev := <-p.Events():
			if ev.Kind == EventPosition {
				return
			}
			if ev.Kind == EventExit {
				t.Fatal("mpv exited before reporting a position")
			}
		case <-deadline:
			t.Fatal("no position event within the deadline")
		}
	}
}
