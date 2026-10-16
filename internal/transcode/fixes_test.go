package transcode

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// The sheet decodes keyframes only, and a failed sheet is not retried on every poll.
func TestSheetSkipsNonKeyframesAndRemembersFailure(t *testing.T) {
	args := sheetArgs("ep.mkv", "fps=1/10", "out.jpg")
	i := slices.Index(args, "-skip_frame")
	if i < 0 || args[i+1] != "nokey" || slices.Index(args, "-i") < i {
		t.Fatalf("args = %v, want -skip_frame nokey before -i", args)
	}

	th := NewThumbnails(filepath.Join(t.TempDir(), "no-ffmpeg"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	dir := t.TempDir()
	th.Generate("ep.mkv", dir, 1400, 16.0/9)
	deadline := time.Now().Add(5 * time.Second)
	for {
		th.mu.Lock()
		_, running := th.running[dir]
		failedAt := th.failed[dir]
		th.mu.Unlock()
		if !running && !failedAt.IsZero() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the failure was not recorded")
		}
		time.Sleep(20 * time.Millisecond)
	}

	th.Generate("ep.mkv", dir, 1400, 16.0/9)
	th.mu.Lock()
	_, running := th.running[dir]
	th.mu.Unlock()
	if running {
		t.Fatal("a sheet that just failed was started again")
	}

	th.mu.Lock()
	th.failed[dir] = time.Now().Add(-thumbRetry - time.Minute)
	th.mu.Unlock()
	th.Generate("ep.mkv", dir, 1400, 16.0/9)
	th.mu.Lock()
	_, running = th.running[dir]
	th.mu.Unlock()
	if !running {
		t.Fatal("an old failure should be tried again")
	}
}

// A hardware encoder failing after it wrote segments: switching to software drops them and the init,
// since libx264's parameters are not the hardware encoder's.
func TestSwitchToSoftwareDropsWhatTheHardwareEncoderWrote(t *testing.T) {
	s := &Session{dir: t.TempDir(), Segments: 6, encoder: "h264_nvenc", Plan: Plan{VideoCodec: "h264_nvenc"},
		log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	os.WriteFile(s.InitPath(), []byte("nvenc"), 0o644)
	for n := range 3 {
		os.WriteFile(s.SegmentPath(n), []byte("x"), 0o644)
	}

	if was := s.switchToSoftware(); was != "h264_nvenc" || s.Plan.VideoCodec != softwareEncoder || !s.fellBack {
		t.Fatalf("was %s, plan %+v, fellBack %v", was, s.Plan, s.fellBack)
	}
	if fileReady(s.InitPath()) {
		t.Error("the hardware encoder's init was kept for libx264's segments")
	}
	for n := range 3 {
		if fileReady(s.SegmentPath(n)) {
			t.Errorf("segment %d from the hardware encoder was kept", n)
		}
	}

	// Nothing written yet: nothing to drop, and the switch is the same.
	fresh := &Session{dir: t.TempDir(), Segments: 6, encoder: "h264_nvenc", Plan: Plan{VideoCodec: "h264_nvenc"}}
	if fresh.switchToSoftware(); fresh.Plan.VideoCodec != softwareEncoder {
		t.Error("the switch did not happen")
	}
}

// Closing a replaced session leaves the replacement's directory, the id still owned.
func TestCloseLeavesAReplacementSessionAlone(t *testing.T) {
	m := &Manager{sessions: map[string]*Session{}}
	dir := t.TempDir()
	old := &Session{ID: "1-1", dir: dir, refs: 1}
	m.sessions["1-1"] = old

	// What Close does under its lock, then a reopen lands before the teardown.
	m.mu.Lock()
	delete(m.sessions, "1-1")
	replacement := &Session{ID: "1-1", dir: dir, refs: 1}
	m.sessions["1-1"] = replacement
	m.mu.Unlock()

	if m.teardown("1-1", old) {
		t.Fatal("teardown said the id is free while a replacement owns it")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatal("the replacement's directory was removed")
	}

	delete(m.sessions, "1-1")
	if !m.teardown("1-1", replacement) {
		t.Fatal("teardown of the last owner should free the id")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("the last owner's directory was kept")
	}
}

// Lines are written beside the picture only for a torrent still arriving; a whole file needs no second process.
func TestLiveSubtitlesOnlyForTorrentSources(t *testing.T) {
	if !liveSubtitlesWanted("http://127.0.0.1:5000/torrents/3/stream/0") {
		t.Error("a torrent source should get live subtitles")
	}
	if liveSubtitlesWanted(`D:\Anime\Show\ep.mkv`) || liveSubtitlesWanted("/api/local/5/stream") {
		t.Error("a whole file should not")
	}
}
