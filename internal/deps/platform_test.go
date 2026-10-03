package deps

import (
	"runtime"
	"slices"
	"strings"
	"testing"
)

// The binary names and archive layout differ by OS, so the mapping is the part
// most likely to be wrong on a platform this machine is not.
func TestComponentFilesPerOS(t *testing.T) {
	cases := []struct {
		name, goos string
		want       []string
	}{
		{"ffmpeg", "windows", []string{"ffmpeg.exe", "ffprobe.exe"}},
		{"ffmpeg", "linux", []string{"ffmpeg", "ffprobe"}},
		{"mpv", "windows", []string{"mpv.exe", "mpv.com"}},
		{"mpv", "linux", []string{"mpv"}},
		{"mpv", "darwin", []string{"mpv"}},
	}
	for _, c := range cases {
		if got := componentFilesFor(c.name, c.goos); !slices.Equal(got, c.want) {
			t.Errorf("%s on %s: files %v, want %v", c.name, c.goos, got, c.want)
		}
	}
}

// Anime4K is always auto-fetched; ffmpeg and mpv depend on the OS. The
// command is only ever shown for what this OS cannot fetch.
func TestManualCommandOnlyForUnfetchable(t *testing.T) {
	if ManualCommand("anime4k") != "" {
		t.Error("anime4k is auto-fetched everywhere; it should have no manual command")
	}
	// The exact command is OS-specific, but on Windows nothing is manual.
	if runtime.GOOS == "windows" {
		for _, name := range []string{"ffmpeg", "mpv"} {
			if ManualCommand(name) != "" {
				t.Errorf("%s is auto-fetched on Windows", name)
			}
		}
	}
}

func TestFfmpegLinuxURL(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64"} {
		url, err := ffmpegLinuxURLFor(arch)
		if err != nil || !strings.HasSuffix(url, ".tar.xz") || !strings.Contains(url, arch) {
			t.Errorf("%s: %q, %v", arch, url, err)
		}
	}
	if _, err := ffmpegLinuxURLFor("riscv64"); err == nil {
		t.Error("an unsupported arch should not resolve a URL")
	}
}
