package transcode

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func binaries(t *testing.T) (ffmpeg, ffprobe string) {
	t.Helper()

	root := os.Getenv("KURO_BIN")
	if root == "" {
		root = filepath.Join("..", "..", "bin")
	}
	suffix := ""
	if os.PathSeparator == '\\' {
		suffix = ".exe"
	}
	ffmpeg = filepath.Join(root, "ffmpeg"+suffix)
	ffprobe = filepath.Join(root, "ffprobe"+suffix)

	for _, p := range []string{ffmpeg, ffprobe} {
		if _, err := os.Stat(p); err != nil {
			t.Skipf("%s not present", p)
		}
	}
	return ffmpeg, ffprobe
}

const testASS = `[Script Info]
ScriptType: v4.00+

[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, Bold
Style: Default,Arial,48,&H00FFFFFF,0

[Events]
Format: Layer, Start, End, Style, Text
Dialogue: 0,0:00:01.00,0:00:05.00,Default,styled line one
Dialogue: 0,0:00:06.00,0:00:10.00,Default,styled line two
`

// A Matroska file with a styled subtitle track and an embedded font, which is
// what anime actually ships as.
func testMKV(t *testing.T, ffmpeg string, seconds int) string {
	t.Helper()
	dir := t.TempDir()

	subPath := filepath.Join(dir, "subs.ass")
	if err := os.WriteFile(subPath, []byte(testASS), 0o644); err != nil {
		t.Fatal(err)
	}
	// -shortest counts the subtitle track too, so a clip is only as long as
	// its last dialogue line unless the track is padded out.
	if seconds > 10 {
		pad := fmt.Sprintf("Dialogue: 0,0:00:%02d.00,0:00:%02d.00,Default,tail\n", seconds-2, seconds-1)
		if err := os.WriteFile(subPath, []byte(testASS+pad), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Any real font file will do; the point is that an attachment exists.
	fontSrc := filepath.Join(os.Getenv("WINDIR"), "Fonts", "arial.ttf")
	if _, err := os.Stat(fontSrc); err != nil {
		fontSrc = ""
	}

	out := filepath.Join(dir, "clip.mkv")
	args := []string{
		"-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=640x360:rate=24:duration=" + itoa(seconds),
		"-f", "lavfi", "-i", "sine=frequency=440:duration=" + itoa(seconds),
		"-i", subPath,
	}
	if fontSrc != "" {
		args = append(args, "-attach", fontSrc,
			"-metadata:s:t:0", "mimetype=application/x-truetype-font",
			"-metadata:s:t:0", "filename=test-font.ttf")
	}
	args = append(args,
		"-map", "0:v", "-map", "1:a", "-map", "2:s",
		"-c:v", "libx264", "-preset", "ultrafast",
		"-c:a", "aac", "-c:s", "copy", "-shortest", out)

	if o, err := exec.Command(ffmpeg, args...).CombinedOutput(); err != nil {
		t.Fatalf("build test mkv: %v\n%s", err, o)
	}
	return out
}

// Encoder failures are otherwise invisible: the only symptom is segments that
// never appear.
type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimSpace(string(p)))
	return len(p), nil
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

func TestProbeReadsEveryStream(t *testing.T) {
	ffmpeg, ffprobe := binaries(t)
	clip := testMKV(t, ffmpeg, 12)

	info, err := NewProber(ffprobe).Probe(context.Background(), clip)
	if err != nil {
		t.Fatal(err)
	}

	if info.Video == nil || info.Video.Codec != "h264" {
		t.Fatalf("video = %+v", info.Video)
	}
	if info.Duration < 10 || info.Duration > 14 {
		t.Errorf("duration = %.1f", info.Duration)
	}
	if len(info.Audio) != 1 {
		t.Errorf("audio streams = %d", len(info.Audio))
	}
	if len(info.Subtitles) != 1 || info.Subtitles[0].Codec != "ass" {
		t.Fatalf("subtitles = %+v", info.Subtitles)
	}
}

// H.264 with AAC needs no re-encoding: only the container is unusable, and
// rewriting that is nearly free.
func TestPlanCopiesPlayableStreams(t *testing.T) {
	ffmpeg, ffprobe := binaries(t)
	clip := testMKV(t, ffmpeg, 6)

	info, err := NewProber(ffprobe).Probe(context.Background(), clip)
	if err != nil {
		t.Fatal(err)
	}

	plan := PlanFor(info, "h264_nvenc")
	if !plan.VideoCopy || !plan.AudioCopy {
		t.Fatalf("plan = %+v, want a pure remux", plan)
	}
	if !strings.Contains(plan.Reason, "remux") {
		t.Errorf("reason = %q", plan.Reason)
	}
}

func TestPlanReEncodesUnplayableStreams(t *testing.T) {
	// 10-bit H.264 has no hardware decoder on any GPU, and HEVC depends on a
	// platform extension that cannot be assumed.
	tests := []struct {
		name  string
		video Stream
		audio string
		wantV bool
	}{
		{"hevc", Stream{Codec: "hevc", BitDepth: 10}, "aac", false},
		{"10-bit h264", Stream{Codec: "h264", BitDepth: 10}, "aac", false},
		{"8-bit h264", Stream{Codec: "h264", BitDepth: 8}, "aac", true},
		{"av1", Stream{Codec: "av1", BitDepth: 10}, "aac", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := &MediaInfo{Video: &tt.video, Audio: []Stream{{Codec: tt.audio}}}
			if got := PlanFor(info, "h264_nvenc").VideoCopy; got != tt.wantV {
				t.Errorf("videoCopy = %v, want %v", got, tt.wantV)
			}
		})
	}

	// Browsers cannot decode these at all.
	for _, codec := range []string{"dts", "truehd", "eac3"} {
		info := &MediaInfo{Video: &Stream{Codec: "h264"}, Audio: []Stream{{Codec: codec}}}
		if PlanFor(info, "").AudioCopy {
			t.Errorf("%s should be re-encoded", codec)
		}
	}
}

func TestSegmentsAreProducedAndSeekable(t *testing.T) {
	ffmpeg, ffprobe := binaries(t)
	clip := testMKV(t, ffmpeg, 30)

	log := slog.New(slog.NewTextHandler(testWriter{t}, &slog.HandlerOptions{Level: slog.LevelDebug}))
	m := NewManager(ffmpeg, ffprobe, t.TempDir(), "libx264", log)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	session, err := m.Open(ctx, "test", clip)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close("test")

	// The playlist covers the whole duration up front so the player has a real
	// seek bar before anything has been encoded.
	playlist := session.Playlist()
	if !strings.Contains(playlist, "#EXT-X-ENDLIST") {
		t.Error("playlist is not marked complete")
	}
	if !strings.Contains(playlist, `#EXT-X-MAP:URI="init.mp4"`) {
		t.Error("fmp4 playlists require an initialisation segment")
	}
	if got := strings.Count(playlist, "#EXTINF"); got != session.Segments {
		t.Errorf("%d segment entries, want %d", got, session.Segments)
	}

	first, err := session.WaitSegment(ctx, 0, 90*time.Second)
	if err != nil {
		t.Fatalf("first segment: %v", err)
	}
	if !fileReady(first) || !fileReady(session.InitPath()) {
		t.Fatal("segment or init file missing")
	}

	// Jumping ahead is the real test: the encoder has to restart at an offset
	// rather than grind through everything in between.
	start := time.Now()
	last := session.Segments - 1
	if _, err := session.WaitSegment(ctx, last, 90*time.Second); err != nil {
		t.Fatalf("seek to segment %d: %v", last, err)
	}
	t.Logf("seek to final segment in %s", time.Since(start).Round(time.Millisecond))
}

func TestSubtitleAndFontExtraction(t *testing.T) {
	ffmpeg, ffprobe := binaries(t)
	clip := testMKV(t, ffmpeg, 12)
	dir := t.TempDir()

	info, err := NewProber(ffprobe).Probe(context.Background(), clip)
	if err != nil {
		t.Fatal(err)
	}

	subs := NewSubtitles(ffmpeg)
	ctx := context.Background()

	path, err := subs.Extract(ctx, clip, dir, info.Subtitles[0].Index, "ass", true)
	if err != nil {
		t.Fatal(err)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Styling has to survive: converting to WebVTT would discard it, which is
	// most of the point of anime subtitles.
	for _, want := range []string{"[V4+ Styles]", "styled line one"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("extracted subtitle missing %q", want)
		}
	}

	// Extracting twice must not re-run ffmpeg or corrupt the file.
	if _, err := subs.Extract(ctx, clip, dir, info.Subtitles[0].Index, "ass", true); err != nil {
		t.Fatal(err)
	}

	if len(info.Attachments) > 0 {
		fonts, err := subs.ExtractFonts(ctx, clip, dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(fonts) == 0 {
			t.Error("no fonts extracted; styled signs would render with substitutes")
		}
	}
}

// A source with no attachments is normal and must not be reported as failure.
func TestExtractFontsWithNoAttachments(t *testing.T) {
	ffmpeg, _ := binaries(t)
	dir := t.TempDir()

	plain := filepath.Join(dir, "plain.mkv")
	exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=320x180:rate=24:duration=3",
		"-c:v", "libx264", "-preset", "ultrafast", plain).Run()

	fonts, err := NewSubtitles(ffmpeg).ExtractFonts(context.Background(), plain, dir)
	if err != nil {
		t.Fatalf("a file with no fonts is not an error: %v", err)
	}
	if len(fonts) != 0 {
		t.Errorf("got %d fonts", len(fonts))
	}
}

func TestRenderable(t *testing.T) {
	for _, codec := range []string{"ass", "ssa", "subrip", "webvtt"} {
		if !Renderable(codec) {
			t.Errorf("%s should be renderable", codec)
		}
	}
	// Bitmap subtitles would have to be burned in, which is deliberately not
	// done.
	for _, codec := range []string{"hdmv_pgs_subtitle", "dvd_subtitle"} {
		if Renderable(codec) {
			t.Errorf("%s is a bitmap format and cannot be rendered as text", codec)
		}
	}
}

// A 10-bit source is re-encoded precisely because the browser cannot decode
// it. Preserving the source depth makes the output equally unplayable.
func TestH264OutputIsAlwaysEightBit(t *testing.T) {
	base := &Session{Info: &MediaInfo{Video: &Stream{Codec: "hevc", BitDepth: 10}}}

	for _, codec := range []string{"h264_nvenc", "h264_qsv", "h264_amf", "libx264"} {
		base.Plan = Plan{VideoCodec: codec}
		args := strings.Join(base.videoArgs(), " ")
		if !strings.Contains(args, "-pix_fmt yuv420p") {
			t.Errorf("%s: output depth not forced to 8-bit: %s", codec, args)
		}
	}
}

// Hardware encoders ignore -force_key_frames, so segment boundaries have to be
// set with the GOP interval or they drift out of alignment.
func TestKeyframeFlagsMatchEncoderKind(t *testing.T) {
	base := &Session{Info: &MediaInfo{Video: &Stream{Codec: "hevc", BitDepth: 10}}}

	base.Plan = Plan{VideoCodec: "h264_nvenc"}
	hw := strings.Join(base.videoArgs(), " ")
	if !strings.Contains(hw, "-g ") || !strings.Contains(hw, "-keyint_min") {
		t.Errorf("hardware encoder needs a fixed gop: %s", hw)
	}
	if strings.Contains(hw, "force_key_frames") {
		t.Errorf("hardware encoders ignore force_key_frames: %s", hw)
	}

	base.Plan = Plan{VideoCodec: "libx264"}
	sw := strings.Join(base.videoArgs(), " ")
	if !strings.Contains(sw, "force_key_frames") {
		t.Errorf("software encoder needs forced keyframes: %s", sw)
	}
	if !strings.Contains(sw, "-sc_threshold 0") {
		t.Errorf("scene cuts would move the forced keyframe: %s", sw)
	}
}

// A copy seek lands on the preceding keyframe, so the offset is biased forward
// to reach the intended one.
func TestRemuxSeekIsBiased(t *testing.T) {
	s := &Session{
		Source: "http://x/1",
		Info:   &MediaInfo{Video: &Stream{Codec: "h264", BitDepth: 8}},
		Plan:   Plan{VideoCopy: true, AudioCopy: true},
	}

	args := strings.Join(s.args(60, 10), " ")
	if !strings.Contains(args, "-ss 60.500") {
		t.Errorf("expected a biased seek: %s", args)
	}
	if !strings.Contains(args, "-start_number 10") {
		t.Errorf("segment numbering must continue from the seek point: %s", args)
	}
	// Without these the segments do not line up with the playlist already
	// given to the player.
	for _, want := range []string{"-copyts", "-hls_segment_type fmp4", "frag_discont"} {
		if !strings.Contains(args, want) {
			t.Errorf("missing %q: %s", want, args)
		}
	}
}
