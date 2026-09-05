package transcode

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The test binary doubles as an ffmpeg that refuses one encoder and passes the
// rest to the real one. A batch file cannot carry "%d.mp4" through cmd.exe intact.
func TestMain(m *testing.M) {
	if real := os.Getenv("KURO_FAKE_FFMPEG"); real != "" {
		os.Exit(fakeFFmpeg(real, os.Getenv("KURO_FAKE_FAIL"), os.Args[1:]))
	}
	os.Exit(m.Run())
}

func fakeFFmpeg(real, refuse string, args []string) int {
	for _, a := range args {
		if a == refuse {
			fmt.Fprintf(os.Stderr, "[%s @ 0] InitializeEncoder failed: invalid param (8)\n", refuse)
			fmt.Fprintf(os.Stderr, "[vost#0:0/%s @ 0] Task finished with error code: -22 (Invalid argument)\n", refuse)
			return 1
		}
	}
	cmd := exec.Command(real, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode()
		}
		return 1
	}
	return 0
}

// fakeBinary points the package at the test binary as ffmpeg, refusing one
// encoder.
func fakeBinary(t *testing.T, ffmpeg, refuse string) string {
	t.Helper()
	t.Setenv("KURO_FAKE_FFMPEG", absolute(ffmpeg))
	t.Setenv("KURO_FAKE_FAIL", refuse)
	return os.Args[0]
}

// tenBitClip is the source that needs re-encoding in the field: 10-bit HEVC.
func tenBitClip(t *testing.T, ffmpeg string, seconds int) string {
	t.Helper()
	out, _ := exec.Command(ffmpeg, "-hide_banner", "-encoders").Output()
	if !strings.Contains(string(out), "libx265") {
		t.Skip("no libx265 in this ffmpeg build")
	}

	path := filepath.Join(t.TempDir(), "hevc10.mkv")
	cmd := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", fmt.Sprintf("testsrc2=size=640x360:rate=24:duration=%d", seconds),
		"-f", "lavfi", "-i", fmt.Sprintf("sine=frequency=440:sample_rate=48000:duration=%d", seconds),
		"-c:v", "libx265", "-preset", "ultrafast", "-pix_fmt", "yuv420p10le",
		"-x265-params", "log-level=error",
		"-c:a", "aac", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("encode 10-bit clip: %v: %s", err, out)
	}
	return path
}

// A hardware encoder that ffmpeg lists but dies at the first frame must be
// noticed, and the job finished in software.
func TestHardwareEncoderFailureFallsBackToSoftware(t *testing.T) {
	ffmpeg, ffprobe := binaries(t)
	clip := tenBitClip(t, ffmpeg, 4)
	fake := fakeBinary(t, ffmpeg, "h264_nvenc")

	m := NewManager(fake, ffprobe, t.TempDir(), "h264_nvenc", slog.Default())
	ctx := context.Background()

	s, err := m.Open(ctx, "1-1", clip)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close("1-1") })
	if s.Plan.VideoCopy || s.Plan.VideoCodec != "h264_nvenc" {
		t.Fatalf("plan should re-encode with the hardware encoder first: %+v", s.Plan)
	}

	path, err := s.WaitSegment(ctx, 0, 90*time.Second)
	if err != nil {
		t.Fatalf("segment 0 never came: %v", err)
	}
	if !fileReady(path) {
		t.Fatal("segment 0 is empty")
	}
	if s.Plan.VideoCodec != softwareEncoder {
		t.Errorf("session still claims %s", s.Plan.VideoCodec)
	}
	if got := m.currentEncoder(); got != softwareEncoder {
		t.Errorf("later sessions would try the broken encoder again: %s", got)
	}
}

// A software failure is not retried: there is nothing left to fall back to,
// and a loop of restarts would hide the real error.
func TestSoftwareFailureIsNotRetried(t *testing.T) {
	const initFail = "InitializeEncoder failed: invalid param (8)"

	s := &Session{Plan: Plan{VideoCodec: softwareEncoder}, dir: t.TempDir()}
	if s.canFallBack(0, initFail) {
		t.Error("libx264 has nothing to fall back to")
	}

	s = &Session{Plan: Plan{VideoCodec: "h264_nvenc"}, dir: t.TempDir(), fellBack: true}
	if s.canFallBack(0, initFail) {
		t.Error("a session that already fell back must not loop")
	}

	// Once a segment exists the encoder worked; whatever killed it was not the
	// encoder.
	s = &Session{Plan: Plan{VideoCodec: "h264_nvenc"}, dir: t.TempDir()}
	os.WriteFile(s.SegmentPath(3), []byte("x"), 0o644)
	if s.canFallBack(3, initFail) {
		t.Error("a pass that produced output is not an encoder failure")
	}
	if !s.canFallBack(4, initFail) {
		t.Error("a pass that produced nothing after an init failure is a fallback")
	}

	// A lost input exits non-zero like an init failure but software cannot help;
	// switching would pin the whole run to libx264 for nothing.
	if s.canFallBack(4, "tcp://127.0.0.1: Connection refused\nError opening input") {
		t.Error("a source read error is not an encoder failure")
	}
}

// Startup detection runs a real encode rather than reading the encoder list,
// because the list is the same on every machine.
func TestDetectEncoderSkipsOneThatCannotEncode(t *testing.T) {
	ffmpeg, _ := binaries(t)
	ctx := context.Background()

	if err := trialEncode(ctx, ffmpeg, softwareEncoder); err != nil {
		t.Fatalf("libx264 should always pass the trial: %v", err)
	}
	if err := trialEncode(ctx, ffmpeg, "h264_nonexistent"); err == nil {
		t.Fatal("an encoder ffmpeg does not have passed the trial")
	}

	fake := fakeBinary(t, ffmpeg, "h264_nvenc")
	if got := DetectEncoder(ctx, fake, slog.Default()); got == "h264_nvenc" {
		t.Error("chose an encoder whose trial encode failed")
	}
}

// A killed pass leaves the segment it was writing short, and a short file was
// being served as whole. Only the last one a pass wrote can be short.
func TestDropTailRemovesOnlyTheLastSegmentOfThePass(t *testing.T) {
	s := &Session{dir: t.TempDir()}
	for _, n := range []int{3, 10, 11, 12} {
		os.WriteFile(s.SegmentPath(n), []byte("x"), 0o644)
	}

	s.dropTail(10)

	for _, n := range []int{3, 10, 11} {
		if !fileReady(s.SegmentPath(n)) {
			t.Errorf("segment %d should have been kept", n)
		}
	}
	if fileReady(s.SegmentPath(12)) {
		t.Error("the last segment of the pass was kept")
	}
}

func TestLastLinesKeepsTheCause(t *testing.T) {
	var lines []string
	for i := range 20 {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	got := lastLines(strings.Join(lines, "\r\n"), encoderLogLines)
	if !strings.Contains(got, "line 10") || strings.Contains(got, "line 9") {
		t.Errorf("wrong window: %s", got)
	}
	if strings.Contains(got, "\r") {
		t.Errorf("carriage returns survived: %q", got)
	}
}

// clipWithCues muxes a subtitle track holding n cues, standing in for a file
// at successive stages of a download.
func clipWithCues(t *testing.T, ffmpeg string, n int) string {
	t.Helper()
	dir := t.TempDir()

	var ass strings.Builder
	ass.WriteString(testASS[:strings.Index(testASS, "Dialogue:")])
	for i := range n {
		fmt.Fprintf(&ass, "Dialogue: 0,0:00:%02d.00,0:00:%02d.50,Default,cue %d\n", i, i, i)
	}
	subs := filepath.Join(dir, "subs.ass")
	if err := os.WriteFile(subs, []byte(ass.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(dir, "clip.mkv")
	cmd := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=64x64:rate=10:duration=1",
		"-i", subs, "-map", "0:v", "-map", "1:s",
		"-c:v", "libx264", "-preset", "ultrafast", "-c:s", "copy", out)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mux clip: %v: %s", err, b)
	}
	return out
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	b, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func backdate(t *testing.T, path string) {
	t.Helper()
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
}

// A track from a still-downloading file stops where the download reached. It
// must be read again while the source can grow, and never replaced by a read
// that recovered less.
func TestPartialTrackIsReadAgainUntilComplete(t *testing.T) {
	ffmpeg, _ := binaries(t)
	s := NewSubtitles(ffmpeg)
	ctx := context.Background()
	dir := t.TempDir()
	source := filepath.Join(dir, "episode.mkv")
	const track = 1

	copyFile(t, clipWithCues(t, ffmpeg, 2), source)
	path, err := s.Extract(ctx, source, dir, track, "ass", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := dialogueLines(path); got != 2 {
		t.Fatalf("first read: %d cues, want 2", got)
	}

	// More of the file arrives. A request straight away is served from the
	// recent read; one later sees the growth.
	copyFile(t, clipWithCues(t, ffmpeg, 5), source)
	if _, err := s.Extract(ctx, source, dir, track, "ass", false); err != nil {
		t.Fatal(err)
	}
	if got := dialogueLines(path); got != 2 {
		t.Errorf("re-read inside the refresh window: %d cues", got)
	}
	backdate(t, path)
	if _, err := s.Extract(ctx, source, dir, track, "ass", false); err != nil {
		t.Fatal(err)
	}
	if got := dialogueLines(path); got != 5 {
		t.Errorf("after the download grew: %d cues, want 5", got)
	}

	// A read that recovers less — holes moved — does not replace the better one.
	copyFile(t, clipWithCues(t, ffmpeg, 3), source)
	backdate(t, path)
	if _, err := s.Extract(ctx, source, dir, track, "ass", false); err != nil {
		t.Fatal(err)
	}
	if got := dialogueLines(path); got != 5 {
		t.Errorf("a shorter read replaced the track: %d cues", got)
	}

	// Complete: read once more and then left alone, however old it gets.
	copyFile(t, clipWithCues(t, ffmpeg, 6), source)
	backdate(t, path)
	if _, err := s.Extract(ctx, source, dir, track, "ass", true); err != nil {
		t.Fatal(err)
	}
	if got := dialogueLines(path); got != 6 {
		t.Errorf("complete read: %d cues, want 6", got)
	}
	backdate(t, path)
	if _, err := s.Extract(ctx, source, dir, track, "ass", true); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(path); time.Since(info.ModTime()) < 30*time.Second {
		t.Error("a complete track was read again")
	}
}
