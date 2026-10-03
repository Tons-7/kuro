package transcode

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// listingFFmpeg is a script answering -version and -encoders like ffmpeg does.
func listingFFmpeg(t *testing.T, encoders string) string {
	t.Helper()
	dir := t.TempDir()
	lines := []string{"ffmpeg version 6.1-test Copyright (c) the FFmpeg developers", encoders}
	if runtime.GOOS == "windows" {
		path := filepath.Join(dir, "ffmpeg.bat")
		var b strings.Builder
		b.WriteString("@echo off\r\n")
		for _, l := range lines {
			b.WriteString("echo " + l + "\r\n")
		}
		if err := os.WriteFile(path, []byte(b.String()), 0o755); err != nil {
			t.Fatal(err)
		}
		return path
	}
	path := filepath.Join(dir, "ffmpeg")
	script := "#!/bin/sh\n" + "printf '%s\\n' '" + strings.Join(lines, "' '") + "'\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// A build without libx264 (Fedora's ffmpeg-free) and no working hardware
// encoder cannot serve a stream; one with it can.
func TestCanEncodeNeedsAnH264Encoder(t *testing.T) {
	ctx := context.Background()
	if CanEncode(ctx, listingFFmpeg(t, " V....D libopenh264           OpenH264 H.264")) {
		t.Error("a build without libx264 counted as able to encode")
	}
	withX264 := listingFFmpeg(t, " V....D libx264              libx264 H.264")
	if !CanEncode(ctx, withX264) {
		t.Error("a build with libx264 counted as unable to encode")
	}
	if got := Version(ctx, withX264); got != "6.1-test" {
		t.Errorf("version = %q", got)
	}
}
