package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kuro/internal/config"
)

func TestSetupExplainsHalfATranscoder(t *testing.T) {
	// Nothing on PATH, so only bin/ decides.
	t.Setenv("PATH", "")
	bin := t.TempDir()
	h := newHarness(t, config.Config{BinDir: bin}, nil)

	if got := h.server.halfTranscoder(); got != "" {
		t.Fatalf("neither installed is plainly not installed, got %q", got)
	}

	os.WriteFile(filepath.Join(bin, config.ExeName("ffmpeg")), []byte("x"), 0o755)
	if got := h.server.halfTranscoder(); !strings.Contains(got, "ffprobe is not") {
		t.Fatalf("ffmpeg alone: %q", got)
	}

	os.WriteFile(filepath.Join(bin, config.ExeName("ffprobe")), []byte("x"), 0o755)
	if got := h.server.halfTranscoder(); got != "" {
		t.Fatalf("both installed needs no explanation, got %q", got)
	}
}
