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
		{"rqbit", "windows", []string{"rqbit.exe"}},
		{"rqbit", "linux", []string{"rqbit"}},
		{"rqbit", "darwin", []string{"rqbit"}},
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

func TestRqbitAssetPerPlatform(t *testing.T) {
	cases := []struct {
		goos, goarch, want string
	}{
		{"windows", "amd64", "rqbit.exe"},
		{"darwin", "arm64", "rqbit-osx-universal"},
		{"darwin", "amd64", "rqbit-osx-universal"},
		{"linux", "amd64", "rqbit-linux-amd64"},
		{"linux", "arm64", "rqbit-linux-arm64"},
	}
	for _, c := range cases {
		got, err := rqbitAssetFor(c.goos, c.goarch)
		if err != nil || got != c.want {
			t.Errorf("%s/%s: %q, %v; want %q", c.goos, c.goarch, got, err, c.want)
		}
	}
	if _, err := rqbitAssetFor("plan9", "amd64"); err == nil {
		t.Error("an unsupported OS should not resolve an asset")
	}
}

// rqbit and Anime4K are always auto-fetched; ffmpeg and mpv depend on the OS.
// The command is only ever shown for what this OS cannot fetch.
func TestManualCommandOnlyForUnfetchable(t *testing.T) {
	for _, name := range []string{"rqbit", "anime4k"} {
		if ManualCommand(name) != "" {
			t.Errorf("%s is auto-fetched everywhere; it should have no manual command", name)
		}
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
