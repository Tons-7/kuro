package torrent

import (
	"io"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func testSupervisor(opts Options) *Supervisor {
	return NewSupervisor(opts, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func has(args []string, flag string) bool { return slices.Contains(args, flag) }

func valueAfter(args []string, flag string) string {
	if i := slices.Index(args, flag); i >= 0 && i+1 < len(args) {
		return args[i+1]
	}
	return ""
}

// The default engine forwards its port (UPnP on) and uploads enough to stay
// unchoked — the settings that decide whether a grab is fast.
func TestRqbitArgsDefaultsFavorSpeed(t *testing.T) {
	s := testSupervisor(Options{})
	args := s.rqbitArgs("/cache")

	if has(args, "--disable-upnp-port-forward") {
		t.Error("UPnP forwarding must be on by default; without it a NAT sees few peers")
	}
	if got := valueAfter(args, "--ratelimit-upload"); got != "4194304" {
		t.Errorf("upload limit = %q, want 4 MiB/s", got)
	}
	// Port and peer limit are left to rqbit unless asked for.
	if has(args, "--listen-port") || has(args, "--peer-limit") {
		t.Error("no port or peer flags should be set by default")
	}
	if args[len(args)-1] != "/cache" {
		t.Errorf("cache dir must be the last arg, got %v", args)
	}
	if !strings.Contains(strings.Join(args, " "), "server start") {
		t.Errorf("missing the server subcommand: %v", args)
	}
}

// A locked-down or metered connection can turn forwarding off and pin the port.
func TestRqbitArgsHonorOverrides(t *testing.T) {
	s := testSupervisor(Options{
		UploadLimit: 512 * 1024, ListenPort: 6881, PeerLimit: 200, DisableUPnP: true,
	})
	args := s.rqbitArgs("/cache")

	if !has(args, "--disable-upnp-port-forward") {
		t.Error("UPnP off should disable forwarding")
	}
	if valueAfter(args, "--listen-port") != "6881" {
		t.Errorf("listen port not passed: %v", args)
	}
	if valueAfter(args, "--peer-limit") != "200" {
		t.Errorf("peer limit not passed: %v", args)
	}
	if valueAfter(args, "--ratelimit-upload") != strconv.Itoa(512*1024) {
		t.Errorf("upload override not passed: %v", args)
	}
}
