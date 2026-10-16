package config

import (
	"context"
	"os/exec"
	"runtime"
	"sync"
	"time"
)

// Window is the app window kuro opened. Closing it with kuro frees the browser
// profile, so a relaunch takes it at once instead of racing a dying window.
type Window struct {
	// Profile is the browser profile directory; empty means AppData.
	Profile string
	// Maximized opens maximized instead of fullscreen, read at each open.
	Maximized func() bool

	// handoff: a second launch, whose browser exits clean at once because the running window took the URL.
	handoff bool

	mu  sync.Mutex
	run *launched
}

func (w *Window) profile() string {
	if w.Profile != "" {
		return w.Profile
	}
	return appProfileDir()
}

type launched struct {
	cmd    *exec.Cmd
	exited chan struct{}
}

// OpenApp, from a second launch, hands the URL to the running kuro's window (a clean quick exit).
func OpenApp(ctx context.Context, url, profile string) {
	(&Window{Profile: profile, handoff: true}).Open(ctx, url)
}

// Open shows the interface in Chromium's app mode (native-looking, no cgo); every step is best-effort.
func (w *Window) Open(ctx context.Context, url string) {
	// The server has only just bound its port; a window that opens first shows a connection error.
	select {
	case <-ctx.Done():
		return
	case <-time.After(250 * time.Millisecond):
	}

	// Twice: right after an update the old window still holds the profile.
	for attempt := range 2 {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(retryWindowAfter):
			}
		}
		maximized := w.Maximized != nil && w.Maximized()
		if l := showWindow(ctx, url, w.profile(), maximized, w.handoff); l != nil {
			w.mu.Lock()
			w.run = l
			w.mu.Unlock()
			return
		}
	}
	showTab(url)
}

// Close ends the window. Bounded: shutdown must not hang on a browser that ignores the kill.
func (w *Window) Close() {
	w.mu.Lock()
	l := w.run
	w.run = nil
	w.mu.Unlock()
	if l == nil || l.cmd == nil || l.cmd.Process == nil {
		return
	}
	l.cmd.Process.Kill()
	select {
	case <-l.exited:
	case <-time.After(3 * time.Second):
	}
}

const retryWindowAfter = 1500 * time.Millisecond

// Swapped in tests, which must not open a browser.
var (
	showWindow = appWindow
	showTab    = openBrowser
)

// appWindow starts the window and reports what stayed up (no close command for a hand-off).
func appWindow(ctx context.Context, url, profile string, maximized, handoff bool) *launched {
	browser := findChromium()
	if browser == "" {
		return nil
	}

	cmd := exec.Command(browser, windowArgs(url, profile, maximized)...)
	if cmd.Start() != nil {
		return nil
	}

	exited := make(chan struct{})
	go func() {
		cmd.Wait()
		close(exited)
	}()

	select {
	case <-exited:
		// Off Windows the window outlives kuro, so a relaunch hands the URL
		// to it and exits clean at once; that is the window, not a failure.
		if (runtime.GOOS != "windows" || handoff) && cmd.ProcessState.Success() {
			return &launched{}
		}
		return nil
	case <-ctx.Done():
		return &launched{cmd: cmd, exited: exited}
	case <-time.After(3 * time.Second):
		return &launched{cmd: cmd, exited: exited}
	}
}

func windowArgs(url, profile string, maximized bool) []string {
	// Fullscreen from the start (F11 leaves it) unless asked for a maximized
	// window; the size is the restore/fallback size if a browser ignores both.
	start := "--start-fullscreen"
	if maximized {
		start = "--start-maximized"
	}
	return []string{
		"--app=" + url,
		// Without a profile of its own the window joins an existing browser session and app mode is ignored.
		"--user-data-dir=" + profile,
		start,
		"--window-size=1440,900",
		"--no-first-run",
		"--no-default-browser-check",
	}
}

func findChromium() string {
	var candidates []string

	switch runtime.GOOS {
	case "windows":
		candidates = []string{
			`C:\Program Files\BraveSoftware\Brave-Browser\Application\brave.exe`,
			`C:\Program Files (x86)\BraveSoftware\Brave-Browser\Application\brave.exe`,
			`C:\Program Files\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
			`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
		}
	case "darwin":
		candidates = []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
		}
	default:
		for _, name := range []string{"google-chrome", "chromium", "chromium-browser", "microsoft-edge", "brave-browser"} {
			if path, err := exec.LookPath(name); err == nil {
				return path
			}
		}
		return ""
	}

	for _, path := range candidates {
		if exists(path) {
			return path
		}
	}
	return ""
}

// openBrowser is the fallback: whatever the system considers the default.
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		// rundll32 avoids cmd's parsing of & in a URL.
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	detach(cmd)
	_ = cmd.Start()
}
