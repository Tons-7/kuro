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
	mu  sync.Mutex
	run *launched
}

type launched struct {
	cmd    *exec.Cmd
	exited chan struct{}
}

// OpenApp shows the interface without keeping a handle to it.
func OpenApp(ctx context.Context, url string) { (&Window{}).Open(ctx, url) }

// Open shows the interface. Chromium's app mode gives a chrome-less window
// that passes for native with no cgo or bundled runtime. Failing to show
// anything is not acceptable, so every step is best-effort.
func (w *Window) Open(ctx context.Context, url string) {
	// The server has only just bound its port; a window that opens first shows
	// a connection error.
	select {
	case <-ctx.Done():
		return
	case <-time.After(250 * time.Millisecond):
	}

	// Twice: right after an update the old window still holds the profile, and
	// a browser that cannot take it exits showing nothing.
	for attempt := range 2 {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(retryWindowAfter):
			}
		}
		if l := showWindow(ctx, url); l != nil {
			w.mu.Lock()
			w.run = l
			w.mu.Unlock()
			return
		}
	}
	showTab(url)
}

// Close ends the window. Bounded: shutdown must not hang on a browser that
// ignores the kill.
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

// appWindow starts the chrome-less window and reports what stayed up; one that
// cannot take the profile exits at once showing nothing. A hand-off to a window
// another process owns is reported without a command to close.
func appWindow(ctx context.Context, url string) *launched {
	browser := findChromium()
	if browser == "" {
		return nil
	}

	cmd := exec.Command(browser,
		"--app="+url,
		// Without a profile of its own the window joins an existing
		// browser session and app mode is ignored.
		"--user-data-dir="+appProfileDir(),
		// Fullscreen from the start (F11 leaves it); the size is the
		// restore/fallback size if a browser ignores the flag.
		"--start-fullscreen",
		"--window-size=1440,900",
		"--no-first-run",
		"--no-default-browser-check",
	)
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
		if runtime.GOOS != "windows" && cmd.ProcessState.Success() {
			return &launched{}
		}
		return nil
	case <-ctx.Done():
		return &launched{cmd: cmd, exited: exited}
	case <-time.After(3 * time.Second):
		return &launched{cmd: cmd, exited: exited}
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
