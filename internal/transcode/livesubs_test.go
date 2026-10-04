package transcode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func liveSession(on bool) *Session {
	return &Session{
		Source: "http://x/1", liveSubs: on,
		Plan: Plan{VideoCopy: true, AudioCopy: true},
		Info: &MediaInfo{
			Video: &Stream{Codec: "h264", BitDepth: 8},
			Subtitles: []Stream{
				{Index: 2, Codec: "ass"}, {Index: 3, Codec: "subrip"}, {Index: 4, Codec: "hdmv_pgs_subtitle"},
			},
		},
	}
}

func TestPassWritesEveryTextTrackBesideThePicture(t *testing.T) {
	args := strings.Join(liveSession(true).args(60, 10), " ")
	for _, want := range []string{
		// ASS is copied; other text is converted; both named after the segment the pass starts at.
		"-map 0:2? -c:s copy -f ass -ignore_readorder 1 -flush_packets 1 -y live-2-10.ass",
		"-map 0:3? -c:s ass -f ass -ignore_readorder 1 -flush_packets 1 -y live-3-10.ass",
	} {
		if !strings.Contains(args, want) {
			t.Errorf("missing %q in: %s", want, args)
		}
	}
	if strings.Contains(args, "live-4-") {
		t.Errorf("a bitmap track cannot be written as text: %s", args)
	}
	// The picture's output comes first and unchanged; the subtitle outputs follow it.
	if strings.Index(args, "playlist.m3u8") > strings.Index(args, "live-2-10.ass") {
		t.Errorf("subtitle output before the playlist: %s", args)
	}
}

func TestPassWithoutLiveSubtitlesIsUnchanged(t *testing.T) {
	if args := strings.Join(liveSession(false).args(0, 0), " "); strings.Contains(args, "live-") {
		t.Errorf("subtitle output though switched off: %s", args)
	}
}

const assHead = "[Script Info]\nTitle: x\n\n[V4+ Styles]\nFormat: Name\nStyle: Default\n\n[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n"

func cue(start, text string) string {
	return "Dialogue: 0," + start + ",0:59:59.00,Default,,0,0,0,," + text + "\n"
}

func TestMergeLiveUnionsEveryPassIntoTheTrack(t *testing.T) {
	dir := t.TempDir()
	track := filepath.Join(dir, "sub-2.ass")
	os.WriteFile(track, []byte(assHead+cue("0:00:01.00", "one")+cue("0:00:05.00", "five")), 0o644)
	os.WriteFile(filepath.Join(dir, "live-2-0.ass"), []byte(assHead+cue("0:00:05.00", "five")+cue("0:00:09.00", "nine")), 0o644)
	// A pass mid-write: its last line has no newline yet and is not a line.
	os.WriteFile(filepath.Join(dir, "live-2-50.ass"), []byte(assHead+cue("0:05:02.00", "later")+"Dialogue: 0,0:05:09.00,0:05:1"), 0o644)
	// Another track's lines stay out.
	os.WriteFile(filepath.Join(dir, "live-3-0.ass"), []byte(assHead+cue("0:00:02.00", "other track")), 0o644)

	subs := NewSubtitles("ffmpeg")
	path, err := subs.MergeLive(dir, 2, "ass")
	if err != nil || path != track {
		t.Fatalf("path %q err %v", path, err)
	}
	got, _ := os.ReadFile(track)
	text := string(got)
	for _, want := range []string{"one", "five", "nine", "later"} {
		if strings.Count(text, ",,"+want+"\n") != 1 {
			t.Errorf("%q appears %d times, want once:\n%s", want, strings.Count(text, ",,"+want+"\n"), text)
		}
	}
	if strings.Contains(text, "other track") || strings.Contains(text, "0:05:09.00") {
		t.Errorf("merged a line it should not have:\n%s", text)
	}
	if n := subs.Cues(dir, 2, "ass"); n != 4 {
		t.Errorf("%d cues, want 4", n)
	}
	if strings.Index(text, "nine") > strings.Index(text, "later") {
		t.Errorf("lines out of time order:\n%s", text)
	}
}

func TestMergeLiveAloneBecomesTheTrack(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "live-2-0.ass"), []byte(assHead+cue("0:00:03.00", "first")), 0o644)

	subs := NewSubtitles("ffmpeg")
	path, err := subs.MergeLive(dir, 2, "ass")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), "Style: Default") || subs.Cues(dir, 2, "ass") != 1 {
		t.Fatalf("track = %q", got)
	}
}

func TestMergeLiveWithNothingNewLeavesTheFileAlone(t *testing.T) {
	dir := t.TempDir()
	track := filepath.Join(dir, "sub-2.ass")
	os.WriteFile(track, []byte(assHead+cue("0:00:01.00", "one")), 0o644)
	before, _ := os.Stat(track)

	if _, err := NewSubtitles("ffmpeg").MergeLive(dir, 2, "ass"); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(track)
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("rewrote a track with nothing to add")
	}
}

func TestLiveSubsFailedTellsItsOwnErrorsFromALostInput(t *testing.T) {
	for detail, want := range map[string]bool{
		"[ass @ 000001] Error writing packet | Conversion failed!":                        true,
		"Could not write header for output file #1 (live-2-0.ass)":                        true,
		"Subtitle encoding failed":                                                        true,
		"[in#0] Error during demuxing: I/O error | Error writing trailer of live-2-0.ass": false,
		"[h264_nvenc @ 0001] OpenEncodeSessionEx failed: out of memory":                   false,
		"": false,
	} {
		if got := liveSubsFailed(detail); got != want {
			t.Errorf("%q = %v, want %v", detail, got, want)
		}
	}
}

func TestLiveCoversOnlyPastWhereThePassBegan(t *testing.T) {
	for _, c := range []struct {
		start, at float64
		want      bool
	}{
		{0, 5, true},      // from the top: everything on screen was read
		{300, 305, false}, // just after a seek: the line on screen began before the pass
		{300, 340, true},
	} {
		if got := LiveCovers(c.start, c.at); got != c.want {
			t.Errorf("LiveCovers(%v, %v) = %v", c.start, c.at, got)
		}
	}
}
