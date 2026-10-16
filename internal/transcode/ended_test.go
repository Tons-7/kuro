package transcode

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// A pass cut off at a segment's start leaves a bare styp, which was served as a segment with no media.
func TestSegmentReadyNeedsMedia(t *testing.T) {
	dir := t.TempDir()
	stub := filepath.Join(dir, "20.mp4")
	os.WriteFile(stub, []byte("\x00\x00\x00\x18stypmsdh\x00\x00\x00\x00msdhmsix"), 0o644)
	whole := filepath.Join(dir, "21.mp4")
	os.WriteFile(whole, []byte("\x00\x00\x00\x18stypmsdh\x00\x00\x00\x00msdhmsix\x00\x00\x00\x10moofmfhd\x00\x00\x00\x00"), 0o644)
	if segmentReady(stub) {
		t.Error("a bare styp counted as a segment")
	}
	if !segmentReady(whole) {
		t.Error("a segment with a fragment was not ready")
	}
	if segmentReady(filepath.Join(dir, "22.mp4")) {
		t.Error("a missing file counted as ready")
	}
}

// ffmpeg exits 0 on a cut source; only its words say the end was not the file's.
func TestEndedEarlyReadsFFmpegsWords(t *testing.T) {
	for detail, want := range map[string]bool{
		"[http @ 0x1] Stream ends prematurely at 52428800, should be 1452868595":            true,
		"[in#0/matroska,webm @ 0x2] Error during demuxing: I/O error":                       true,
		"[matroska,webm @ 0x3] Read error at pos. 52428800 (0x3200000): Input/output error": true,
		"": false,
		"[aac @ 0x4] Too many bits per frame requested": false,
	} {
		if got := endedEarly(detail); got != want {
			t.Errorf("endedEarly(%q) = %v, want %v", detail, got, want)
		}
	}
}

// A file on disk with a damaged tail lost its last segment on every pass, and that segment never came.
func TestOnlyAStreamIsCutShort(t *testing.T) {
	cut := "[in#0/matroska,webm @ 0x2] Error during demuxing: I/O error"
	if !cutShort("http://127.0.0.1:1234/torrents/1/stream/0", cut) {
		t.Error("the engine's stream cut short was taken as whole")
	}
	if cutShort(`D:\Anime\Show - 01.mkv`, cut) {
		t.Error("a file on disk was taken as cut short; it can't arrive later")
	}
}

// Reconnect options go on the engine's HTTP stream only: a file input rejects them and ffmpeg would not start.
func TestReconnectOnlyForHTTP(t *testing.T) {
	if got := reconnectArgs("http://127.0.0.1:1234/torrents/1/stream/0"); !slices.Contains(got, "-reconnect") {
		t.Errorf("no reconnect for the engine stream: %v", got)
	}
	if got := reconnectArgs(`D:\Anime\Show - 01.mkv`); got != nil {
		t.Errorf("reconnect options on a file: %v", got)
	}
	s := &Session{Source: "http://127.0.0.1:1234/torrents/1/stream/0", Plan: Plan{VideoCopy: true, AudioCopy: true}}
	args := s.args(0, 0)
	if i, j := slices.Index(args, "-reconnect"), slices.Index(args, "-i"); i < 0 || i > j {
		t.Errorf("reconnect must come before -i: %v", args)
	}
}
