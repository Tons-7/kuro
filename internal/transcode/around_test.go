package transcode

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A minute of video with a line every two seconds.
func minuteWithCues(t *testing.T, ffmpeg string) string {
	t.Helper()
	dir := t.TempDir()
	var ass strings.Builder
	ass.WriteString(testASS[:strings.Index(testASS, "Dialogue:")])
	for s := 0; s < 60; s += 2 {
		fmt.Fprintf(&ass, "Dialogue: 0,0:00:%02d.00,0:00:%02d.50,Default,,0,0,0,,cue %d\n", s, s, s)
	}
	subs := filepath.Join(dir, "subs.ass")
	if err := os.WriteFile(subs, []byte(ass.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "minute.mkv")
	cmd := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=64x64:rate=10:duration=60",
		"-i", subs, "-map", "0:v", "-map", "1:s",
		"-c:v", "libx264", "-preset", "ultrafast", "-g", "20", "-c:s", "copy", out)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mux: %v: %s", err, b)
	}
	return out
}

// Reading around the playhead adds that stretch's lines, at their real times,
// to what an earlier read found, without losing any of it.
func TestExtractAroundMergesThePlayheadStretch(t *testing.T) {
	ffmpeg, _ := binaries(t)
	clip := minuteWithCues(t, ffmpeg)
	dir := t.TempDir()
	s := NewSubtitles(ffmpeg)
	path := filepath.Join(dir, "sub-1.ass")

	// What a read from the start got before the download ran out.
	early := testASS[:strings.Index(testASS, "Dialogue:")] +
		"Dialogue: 0,0:00:00.00,0:00:00.50,Default,,0,0,0,,cue 0\n"
	if err := os.WriteFile(path, []byte(early), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := s.ExtractAround(context.Background(), clip, dir, 1, "ass", 40)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(got)
	text := string(body)
	for _, want := range []string{",cue 0", "0:00:40.00", ",cue 40", ",cue 58"} {
		if !strings.Contains(text, want) {
			t.Errorf("track lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, ",cue 2\n") {
		t.Error("read far behind the playhead instead of around it")
	}
}
