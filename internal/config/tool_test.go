package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestExeNameSuffixesOnlyOnWindows(t *testing.T) {
	got := ExeName("ffmpeg")
	want := "ffmpeg"
	if runtime.GOOS == "windows" {
		want = "ffmpeg.exe"
	}
	if got != want {
		t.Errorf("ExeName = %q, want %q", got, want)
	}
}

// Tool prefers a binary kuro downloaded into bin/, then one on PATH, so a
// system install (brew/apt) works with nothing in bin/.
func TestToolPrefersBinThenPath(t *testing.T) {
	bin := t.TempDir()
	c := Config{BinDir: bin}

	// A tool nowhere falls back to the bin path, so an error names where kuro
	// looked rather than an empty string.
	if got := c.Tool("nedb-nonesuch"); got != filepath.Join(bin, ExeName("nedb-nonesuch")) {
		t.Errorf("missing tool = %q, want the bin path", got)
	}

	// One in bin/ wins even if a same-named tool is on PATH.
	name := "kuro-tool-test"
	inBin := filepath.Join(bin, ExeName(name))
	if err := os.WriteFile(inBin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := c.Tool(name); got != inBin {
		t.Errorf("bin tool = %q, want %q", got, inBin)
	}

	// One only on PATH is found there.
	pathDir := t.TempDir()
	onPath := filepath.Join(pathDir, ExeName("kuro-path-tool"))
	if err := os.WriteFile(onPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", pathDir)
	if got := (Config{BinDir: bin}).Tool("kuro-path-tool"); got != onPath {
		t.Errorf("path tool = %q, want %q", got, onPath)
	}
}
