package deps

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Opt-in: KURO_LIVE=1 [KURO_LIVE_DEPS=ffmpeg] go test ./internal/deps -run LiveInstall -timeout 60m
func TestLiveInstallUnpacksRealSevenZips(t *testing.T) {
	if os.Getenv("KURO_LIVE") == "" {
		t.Skip("set KURO_LIVE=1 to download mpv and ffmpeg")
	}
	bin := t.TempDir()
	m := New(bin, slog.New(slog.NewTextHandler(io.Discard, nil)))

	names := []string{"mpv", "ffmpeg"}
	if only := os.Getenv("KURO_LIVE_DEPS"); only != "" {
		names = strings.Split(only, ",")
	}
	for _, name := range names {
		if err := m.Install(name); err != nil {
			t.Fatal(err)
		}
	}

	deadline := time.Now().Add(55 * time.Minute)
	for time.Now().Before(deadline) {
		done := 0
		for _, p := range m.Status() {
			switch p.Stage {
			case StageFailed:
				t.Fatalf("%s: %s", p.Component, p.Error)
			case StageDone:
				done++
			}
		}
		if done == len(names) {
			break
		}
		time.Sleep(2 * time.Second)
	}

	for _, name := range names {
		for _, want := range componentFiles(name) {
			if _, err := os.Stat(filepath.Join(bin, want)); err != nil {
				t.Errorf("%s not installed: %v", want, err)
			}
		}
	}
}
