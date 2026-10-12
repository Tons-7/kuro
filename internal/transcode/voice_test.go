package transcode

import (
	"context"
	"fmt"
	"io"
	"kuro/internal/sound"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestVoiceForcesAnEncodeAndShapesTheFilterByLayout(t *testing.T) {
	stereo := &MediaInfo{Video: &Stream{Codec: "h264", BitDepth: 8}, Audio: []Stream{{Codec: "aac", Channels: 2}}}
	s := &Session{Info: stereo, encoder: "libx264", Plan: PlanFor(stereo, "libx264")}
	if !s.Plan.AudioCopy || strings.Contains(strings.Join(s.args(0, 0), " "), "-af") {
		t.Fatal("off: an aac track is copied and no filter is set")
	}

	s.sound = sound.Options{Voice: "medium"}
	s.Plan = planFor(stereo, "libx264", 0, sound.Options{Voice: "medium"})
	args := strings.Join(s.args(0, 0), " ")
	if s.Plan.AudioCopy || s.Plan.AudioCodec != "aac" || s.Plan.Sound.Voice != "medium" {
		t.Errorf("plan = %+v, want an aac encode with the lift", s.Plan)
	}
	if !strings.Contains(args, "dialoguenhance=") || !strings.Contains(args, "alimiter") || strings.Contains(args, " -ac ") {
		t.Errorf("stereo args = %s", args)
	}

	surround := &MediaInfo{Video: &Stream{Codec: "h264", BitDepth: 8}, Audio: []Stream{{Codec: "truehd", Channels: 6}}}
	s = &Session{Info: surround, encoder: "libx264", sound: sound.Options{Voice: "high"}, Plan: planFor(surround, "libx264", 0, sound.Options{Voice: "high"})}
	args = strings.Join(s.args(0, 0), " ")
	if s.Plan.AudioCodec != "flac" {
		t.Errorf("a lossless source stays lossless under the lift: %+v", s.Plan)
	}
	if !strings.Contains(args, "pan=stereo|FL=0.414*FL+1.166*FC") || strings.Contains(args, "dialoguenhance") {
		t.Errorf("surround args = %s", args)
	}

	mono := &MediaInfo{Audio: []Stream{{Codec: "aac", Channels: 1}}}
	s = &Session{Info: mono, sound: sound.Options{Voice: "medium"}, Plan: planFor(mono, "", 0, sound.Options{Voice: "medium"})}
	if f := s.soundFilter(); strings.Contains(f, "pan=") || strings.Contains(f, "dialoguenhance") {
		t.Errorf("mono has nothing to lift the voice over: %s", f)
	}
}

func TestSetVoiceAcceptsOnlyTheLevelsAndNotesAChange(t *testing.T) {
	s := &Session{Info: &MediaInfo{Audio: []Stream{{Codec: "aac", Channels: 2}}}, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if s.SetVoice("loud") {
		t.Error("an unknown level was accepted")
	}
	if s.SetVoice("off") {
		t.Error("off when already off is a change")
	}
	if !s.SetVoice("medium") || s.Voice() != "medium" || s.Plan.AudioCopy {
		t.Errorf("medium did not take: voice=%q plan=%+v", s.Voice(), s.Plan)
	}
	if s.SetVoice("medium") {
		t.Error("the same level again is a change")
	}
	if !s.SetVoice("off") || s.Voice() != "off" {
		t.Error("off did not take")
	}
}

// bandLevel is the RMS level, in dB, of the file's sound within a narrow band around hz.
func bandLevel(t *testing.T, ffmpeg, file string, hz int) float64 {
	t.Helper()
	out, err := exec.Command(ffmpeg, "-hide_banner", "-i", file,
		"-af", fmt.Sprintf("bandpass=f=%d:width_type=h:w=150,astats=measure_perchannel=none:measure_overall=RMS_level", hz),
		"-f", "null", "-").CombinedOutput()
	if err != nil {
		t.Fatalf("measure %d Hz: %v\n%s", hz, err, out)
	}
	m := regexp.MustCompile(`RMS level dB:\s*(-?[\d.]+)`).FindStringSubmatch(string(out))
	if m == nil {
		t.Fatalf("no RMS level for %d Hz in:\n%s", hz, out)
	}
	v, _ := strconv.ParseFloat(m[1], 64)
	return v
}

// firstSegment plays a clip through a session at the given lift and returns the first segment as a file.
func firstSegment(t *testing.T, ffmpeg, ffprobe, clip, level string) string {
	t.Helper()
	m := NewManager(ffmpeg, ffprobe, t.TempDir(), "libx264", slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	id := "voice-" + level
	session, err := m.Open(ctx, id, clip)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close(id)
	session.SetVoice(level)
	if _, err := session.WaitSegment(ctx, 0, 90*time.Second); err != nil {
		t.Fatalf("segment 0 at %s: %v", level, err)
	}
	init, _ := os.ReadFile(session.InitPath())
	body, _ := os.ReadFile(session.SegmentPath(0))
	whole := filepath.Join(t.TempDir(), level+".mp4")
	os.WriteFile(whole, append(init, body...), 0o644)
	return whole
}

// voiceOverMusic is how far the voice band stands above the music band in a segment.
func voiceOverMusic(t *testing.T, ffmpeg, ffprobe, clip, level string, voiceHz, musicHz int) float64 {
	t.Helper()
	seg := firstSegment(t, ffmpeg, ffprobe, clip, level)
	return bandLevel(t, ffmpeg, seg, voiceHz) - bandLevel(t, ffmpeg, seg, musicHz)
}

// A 5.1 mix with the voice (a 1 kHz tone) in the centre and music (3 kHz) in the fronts: the lift
// moves only the centre, by the dB it promises.
func TestVoiceLiftsTheCentreOfASurroundMix(t *testing.T) {
	ffmpeg, ffprobe := binaries(t)
	clip := filepath.Join(t.TempDir(), "surround.mkv")
	if o, err := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=320x240:rate=24:duration=14",
		"-f", "lavfi", "-i", "aevalsrc=0.2*sin(3000*2*PI*t)|0.2*sin(3000*2*PI*t)|0.2*sin(1000*2*PI*t)|0|0|0:c=5.1:s=48000:d=14",
		"-map", "0:v", "-map", "1:a", "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
		"-c:a", "ac3", clip).CombinedOutput(); err != nil {
		t.Fatalf("build clip: %v\n%s", err, o)
	}

	off := voiceOverMusic(t, ffmpeg, ffprobe, clip, "off", 1000, 3000)
	medium := voiceOverMusic(t, ffmpeg, ffprobe, clip, "medium", 1000, 3000)
	high := voiceOverMusic(t, ffmpeg, ffprobe, clip, "high", 1000, 3000)
	t.Logf("voice over music: off %.1f dB, medium %.1f dB, high %.1f dB", off, medium, high)
	// The centre gain plus the presence lift's share at 1 kHz, about a dB.
	if d := medium - off; d < 5.5 || d > 8.5 {
		t.Errorf("medium lifts the voice by %.1f dB, want about 6 plus presence", d)
	}
	if d := high - off; d < 11 || d > 15 {
		t.Errorf("high lifts the voice by %.1f dB, want about 12 plus presence", d)
	}
}

// Stereo: a voice-like, centred tone (both channels alike) against music spread wide (opposite
// phase on the two sides). The extraction finds the centred voice and lifts it, leaving the music.
func TestVoiceLiftsACentredVoiceInStereo(t *testing.T) {
	ffmpeg, ffprobe := binaries(t)
	clip := filepath.Join(t.TempDir(), "stereo.mkv")
	// Speech changes its spectrum all the time; a steady tone does not, so the voice is frequency-modulated.
	voice := "0.2*sin(300*2*PI*t+15*sin(5*2*PI*t))"
	if o, err := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=320x240:rate=24:duration=14",
		"-f", "lavfi", "-i", fmt.Sprintf("aevalsrc=%s+0.2*sin(3000*2*PI*t)|%s-0.2*sin(3000*2*PI*t):c=stereo:s=48000:d=14", voice, voice),
		"-map", "0:v", "-map", "1:a", "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
		"-c:a", "aac", clip).CombinedOutput(); err != nil {
		t.Fatalf("build clip: %v\n%s", err, o)
	}

	off := voiceOverMusic(t, ffmpeg, ffprobe, clip, "off", 300, 3000)
	medium := voiceOverMusic(t, ffmpeg, ffprobe, clip, "medium", 300, 3000)
	high := voiceOverMusic(t, ffmpeg, ffprobe, clip, "high", 300, 3000)
	t.Logf("voice over music: off %.1f dB, medium %.1f dB, high %.1f dB", off, medium, high)
	if d := medium - off; d < 3 {
		t.Errorf("medium lifts the centred voice by only %.1f dB", d)
	}
	if d := high - off; d < medium-off+2 {
		t.Errorf("high (%.1f dB) is not clearly more than medium (%.1f dB)", high-off, medium-off)
	}
}

// Lifted sound stays in step with the picture: the filters delay nothing the player can notice.
func TestVoiceKeepsSoundInStepWithThePicture(t *testing.T) {
	ffmpeg, ffprobe := binaries(t)
	clip := filepath.Join(t.TempDir(), "clip.mkv")
	if o, err := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=640x360:rate=24:duration=40",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=40",
		"-map", "0:v", "-map", "1:a", "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-g", "48",
		"-c:a", "aac", "-ac", "2", clip).CombinedOutput(); err != nil {
		t.Fatalf("build clip: %v\n%s", err, o)
	}
	m := NewManager(ffmpeg, ffprobe, t.TempDir(), "libx264", slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	session, err := m.Open(ctx, "voice-sync", clip)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close("voice-sync")
	session.SetVoice("high")
	for n := range 3 {
		if _, err := session.WaitSegment(ctx, n, 90*time.Second); err != nil {
			t.Fatalf("segment %d: %v", n, err)
		}
	}
	for n := range 2 {
		video, audio := lastPacket(t, ffprobe, session, n, "v:0"), lastPacket(t, ffprobe, session, n, "a:0")
		if audio < video-1 {
			t.Errorf("segment %d: picture runs to %.2fs but sound only to %.2fs", n, video, audio)
		}
	}
}

// segmentWith plays the clip with the sound asked for and returns the first segment as a file.
func segmentWith(t *testing.T, ffmpeg, ffprobe, clip string, o sound.Options) string {
	t.Helper()
	m := NewManager(ffmpeg, ffprobe, t.TempDir(), "libx264", slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	id := fmt.Sprintf("sound-%v-%v-%v-%s", o.Night, o.Headphones, o.Mono, o.Level())
	session, err := m.Open(ctx, id, clip)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close(id)
	session.SetSound(o)
	if _, err := session.WaitSegment(ctx, 0, 90*time.Second); err != nil {
		t.Fatalf("segment 0: %v", err)
	}
	init, _ := os.ReadFile(session.InitPath())
	body, _ := os.ReadFile(session.SegmentPath(0))
	whole := filepath.Join(t.TempDir(), "seg.mp4")
	os.WriteFile(whole, append(init, body...), 0o644)
	return whole
}

// channelLevel is the RMS level, in dB, of one channel (FL or FR) over a stretch of the file.
func channelLevel(t *testing.T, ffmpeg, file, channel string, from, to float64) float64 {
	t.Helper()
	out, err := exec.Command(ffmpeg, "-hide_banner", "-ss", fmt.Sprint(from), "-to", fmt.Sprint(to), "-i", file,
		"-af", fmt.Sprintf("pan=mono|c0=%s,astats=measure_perchannel=none:measure_overall=RMS_level", channel),
		"-f", "null", "-").CombinedOutput()
	if err != nil {
		t.Fatalf("measure %s: %v\n%s", channel, err, out)
	}
	m := regexp.MustCompile(`RMS level dB:\s*(-?[\d.]+|-inf)`).FindStringSubmatch(string(out))
	if m == nil {
		t.Fatalf("no RMS level for %s in:\n%s", channel, out)
	}
	if m[1] == "-inf" {
		return -120
	}
	v, _ := strconv.ParseFloat(m[1], 64)
	return v
}

// Night mode narrows the gap between a loud stretch and a quiet one.
func TestNightModeNarrowsLoudAndQuiet(t *testing.T) {
	ffmpeg, ffprobe := binaries(t)
	clip := filepath.Join(t.TempDir(), "dyn.mkv")
	// Loud for the first two seconds, quiet for the next two, and so on.
	if o, err := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=320x240:rate=24:duration=12",
		"-f", "lavfi", "-i", "aevalsrc=(0.425+0.375*sgn(sin(PI*t/2)))*sin(440*2*PI*t)|(0.425+0.375*sgn(sin(PI*t/2)))*sin(440*2*PI*t):c=stereo:s=48000:d=12",
		"-map", "0:v", "-map", "1:a", "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
		"-c:a", "aac", clip).CombinedOutput(); err != nil {
		t.Fatalf("build clip: %v\n%s", err, o)
	}
	gap := func(o sound.Options) float64 {
		seg := segmentWith(t, ffmpeg, ffprobe, clip, o)
		return channelLevel(t, ffmpeg, seg, "FL", 0.5, 1.5) - channelLevel(t, ffmpeg, seg, "FL", 2.5, 3.5)
	}
	plain, night := gap(sound.Options{Night: true}), 0.0
	night, plain = plain, gap(sound.Options{Mono: true})
	t.Logf("loud over quiet: as is %.1f dB, night mode %.1f dB", plain, night)
	if plain < 20 {
		t.Fatalf("the clip's own gap is only %.1f dB; the test proves nothing", plain)
	}
	if night > plain-6 {
		t.Errorf("night mode left a %.1f dB gap, want it well under %.1f", night, plain)
	}
}

// Mono plays both channels in both ears; headphones mode bleeds a little of a hard-left sound to the right.
func TestMonoAndHeadphonesReachTheOtherEar(t *testing.T) {
	ffmpeg, ffprobe := binaries(t)
	clip := filepath.Join(t.TempDir(), "left.mkv")
	if o, err := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=320x240:rate=24:duration=10",
		"-f", "lavfi", "-i", "aevalsrc=0.5*sin(300*2*PI*t)|0:c=stereo:s=48000:d=10",
		"-map", "0:v", "-map", "1:a", "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
		"-c:a", "aac", clip).CombinedOutput(); err != nil {
		t.Fatalf("build clip: %v\n%s", err, o)
	}
	right := func(o sound.Options) (left, right float64) {
		seg := segmentWith(t, ffmpeg, ffprobe, clip, o)
		return channelLevel(t, ffmpeg, seg, "FL", 1, 5), channelLevel(t, ffmpeg, seg, "FR", 1, 5)
	}

	l, r := right(sound.Options{Night: true})
	t.Logf("as is: left %.1f dB, right %.1f dB", l, r)
	if r > l-30 {
		t.Fatalf("the right channel is not silent to begin with (%.1f vs %.1f); the test proves nothing", r, l)
	}
	l, r = right(sound.Options{Mono: true})
	t.Logf("mono: left %.1f dB, right %.1f dB", l, r)
	if d := l - r; d < -1 || d > 1 {
		t.Errorf("mono left the ears %.1f dB apart", d)
	}
	l, r = right(sound.Options{Headphones: true})
	t.Logf("headphones: left %.1f dB, right %.1f dB", l, r)
	if r < l-20 || r > l-3 {
		t.Errorf("headphones mode put the left sound in the right ear at %.1f dB below, want some but less", l-r)
	}
}

// The volume boost raises the whole mix by the dB it promises, apart from the voice.
func TestVolumeBoostRaisesTheWholeMix(t *testing.T) {
	ffmpeg, ffprobe := binaries(t)
	clip := filepath.Join(t.TempDir(), "quiet.mkv")
	if o, err := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=320x240:rate=24:duration=10",
		"-f", "lavfi", "-i", "aevalsrc=0.1*sin(440*2*PI*t)|0.1*sin(440*2*PI*t):c=stereo:s=48000:d=10",
		"-map", "0:v", "-map", "1:a", "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
		"-c:a", "aac", clip).CombinedOutput(); err != nil {
		t.Fatalf("build clip: %v\n%s", err, o)
	}
	level := func(o sound.Options) float64 {
		return channelLevel(t, ffmpeg, segmentWith(t, ffmpeg, ffprobe, clip, o), "FL", 1, 5)
	}
	plain, medium, high := level(sound.Options{Mono: true}), level(sound.Options{Boost: "medium"}), level(sound.Options{Boost: "high"})
	t.Logf("level: as is %.1f dB, medium %.1f dB, high %.1f dB", plain, medium, high)
	if d := medium - plain; d < 5 || d > 7 {
		t.Errorf("medium raised the mix by %.1f dB, want 6", d)
	}
	if d := high - plain; d < 11 || d > 13 {
		t.Errorf("high raised the mix by %.1f dB, want 12", d)
	}
}
