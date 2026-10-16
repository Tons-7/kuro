package deps

import (
	"fmt"
	"runtime"
)

// exeExtFor is the suffix an executable carries on that OS.
func exeExtFor(goos string) string {
	if goos == "windows" {
		return ".exe"
	}
	return ""
}

// componentFiles is the executables a component installs here (mpv on Windows adds a .com shim).
func componentFiles(name string) []string { return componentFilesFor(name, runtime.GOOS) }

func componentFilesFor(name, goos string) []string {
	ext := exeExtFor(goos)
	switch name {
	case "ffmpeg":
		return []string{"ffmpeg" + ext, "ffprobe" + ext}
	case "mpv":
		if goos == "windows" {
			return []string{"mpv.exe", "mpv.com"}
		}
		return []string{"mpv"}
	}
	return nil
}

// ffmpegLinuxURL is John Van Sickle's static build: ffmpeg and ffprobe in one tar.xz.
func ffmpegLinuxURL() (string, error) { return ffmpegLinuxURLFor(runtime.GOARCH) }

func ffmpegLinuxURLFor(goarch string) (string, error) {
	switch goarch {
	case "amd64":
		return "https://johnvansickle.com/ffmpeg/releases/ffmpeg-release-amd64-static.tar.xz", nil
	case "arm64":
		return "https://johnvansickle.com/ffmpeg/releases/ffmpeg-release-arm64-static.tar.xz", nil
	case "386":
		return "https://johnvansickle.com/ffmpeg/releases/ffmpeg-release-i686-static.tar.xz", nil
	case "arm":
		return "https://johnvansickle.com/ffmpeg/releases/ffmpeg-release-armhf-static.tar.xz", nil
	}
	return "", fmt.Errorf("no static ffmpeg build for linux/%s", goarch)
}

// ManualCommand is the package-manager command for a component kuro cannot fetch here, or "".
func ManualCommand(name string) string {
	if runtime.GOOS == "windows" || name == "anime4k" {
		return ""
	}
	if name == "ffmpeg" && runtime.GOOS == "linux" {
		if _, err := ffmpegLinuxURL(); err == nil {
			return "" // John Van Sickle static build is fetched.
		}
	}
	switch runtime.GOOS {
	case "darwin":
		return "brew install " + name
	case "linux":
		return "sudo apt install " + name
	}
	return ""
}

// manualInstall: the component comes from the package manager and is then found on PATH.
func manualInstall(name string) error {
	how := ManualCommand(name)
	if how == "" {
		how = "your package manager"
	}
	return fmt.Errorf("kuro cannot download %s on %s; install it with `%s` (kuro finds it on PATH)",
		name, runtime.GOOS, how)
}
