package player

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
)

// Every launch names its own IPC socket, so a dying mpv or a second kuro can never answer for the new one.
func TestEachLaunchGetsItsOwnSocket(t *testing.T) {
	a, b := uniqueSocket(`\\.\pipe\kuro-mpv`, 1), uniqueSocket(`\\.\pipe\kuro-mpv`, 2)
	if a == b || !strings.HasPrefix(a, `\\.\pipe\kuro-mpv-`) {
		t.Fatalf("pipes %q and %q", a, b)
	}
	u := uniqueSocket("/tmp/kuro-mpv.sock", 3)
	if !strings.HasPrefix(u, "/tmp/kuro-mpv-") || !strings.HasSuffix(u, "-3.sock") {
		t.Fatalf("unix socket %q", u)
	}

	m := New(filepath.Join(t.TempDir(), "missing-mpv"), "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	first := m.socket
	m.Play(context.Background(), Options{URL: "x"})
	second := m.socket
	m.Play(context.Background(), Options{URL: "x"})
	if second == first || m.socket == second {
		t.Fatalf("sockets %q, %q, %q: not one per launch", first, second, m.socket)
	}
}
