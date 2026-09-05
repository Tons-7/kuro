package deps

import (
	"fmt"
	"runtime"
)

// exeExt is the suffix an executable carries on this OS.
func exeExt() string { return exeExtFor(runtime.GOOS) }

func exeExtFor(goos string) string {
	if goos == "windows" {
		return ".exe"
	}
	return ""
}

// componentFiles is the executables a component installs on this OS. mpv on
// Windows ships a console shim (.com) beside the GUI binary; elsewhere it is
// one file, and a component with no auto-download here returns nothing.
func componentFiles(name string) []string { return componentFilesFor(name, runtime.GOOS) }

func componentFilesFor(name, goos string) []string {
	ext := exeExtFor(goos)
	switch name {
	case "rqbit":
		return []string{"rqbit" + ext}
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

// rqbitAsset is the release asset for this OS/arch. rqbit publishes a plain
// binary per platform, so there is no archive to unpack.
func rqbitAsset() (string, error) { return rqbitAssetFor(runtime.GOOS, runtime.GOARCH) }

func rqbitAssetFor(goos, goarch string) (string, error) {
	switch goos {
	case "windows":
		return "rqbit.exe", nil
	case "darwin":
		return "rqbit-osx-universal", nil
	case "linux":
		switch goarch {
		case "amd64":
			return "rqbit-linux-amd64", nil
		case "arm64":
			return "rqbit-linux-arm64", nil
		case "arm":
			return "rqbit-linux-arm-v7", nil
		}
	}
	return "", fmt.Errorf("no rqbit build for %s/%s", goos, goarch)
}

// ffmpegLinuxURL is John Van Sickle's static build, which bundles ffmpeg and
// ffprobe in one tar.xz the system tar unpacks.
func ffmpegLinuxURL() (string, error) { return ffmpegLinuxURLFor(runtime.GOARCH) }

func ffmpegLinuxURLFor(goarch string) (string, error) {
	switch goarch {
	case "amd64":
		return "https://johnvansickle.com/ffmpeg/releases/ffmpeg-release-amd64-static.tar.xz", nil
	case "arm64":
		return "https://johnvansickle.com/ffmpeg/releases/ffmpeg-release-arm64-static.tar.xz", nil
	}
	return "", fmt.Errorf("no static ffmpeg build for linux/%s", goarch)
}

// ManualCommand is the package-manager command for a component kuro cannot
// fetch on this OS, or "" when it can. Anime4K and, on Windows, everything is
// auto-fetched; ffmpeg on macOS and mpv on every non-Windows OS are not.
func ManualCommand(name string) string {
	if runtime.GOOS == "windows" || name == "anime4k" || name == "rqbit" {
		return ""
	}
	if name == "ffmpeg" && runtime.GOOS == "linux" {
		return "" // John Van Sickle static build is fetched.
	}
	switch runtime.GOOS {
	case "darwin":
		return "brew install " + name
	case "linux":
		return "sudo apt install " + name
	}
	return ""
}

// manualInstall is the resolver error for a component that must come from the
// system package manager; the binary is then found on PATH.
func manualInstall(name string) error {
	how := ManualCommand(name)
	if how == "" {
		how = "your package manager"
	}
	return fmt.Errorf("kuro cannot download %s on %s; install it with `%s` (kuro finds it on PATH)",
		name, runtime.GOOS, how)
}
