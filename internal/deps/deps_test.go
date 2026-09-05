package deps

import (
	"archive/zip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestKnownComponents(t *testing.T) {
	for _, name := range []string{"rqbit", "ffmpeg", "mpv", "anime4k"} {
		if !Known(name) {
			t.Errorf("%s should be installable", name)
		}
	}
	if Known("definitely-not-a-component") {
		t.Error("an unknown name must not be installable")
	}
}

// Git and MSYS both put GNU tar ahead of Windows' own on PATH, and GNU tar
// cannot read 7-Zip — which is what ffmpeg and mpv ship in.
func TestSystemTarIsNotWhateverIsOnPath(t *testing.T) {
	got := systemTar()
	if runtime.GOOS != "windows" {
		if got != "tar" {
			t.Errorf("systemTar() = %q, want tar", got)
		}
		return
	}
	if !strings.Contains(strings.ToLower(got), "system32") {
		t.Errorf("systemTar() = %q; it should name Windows' own bsdtar", got)
	}
}

func writeZip(t *testing.T, path string, entries map[string]string) {
	t.Helper()

	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	w := zip.NewWriter(f)
	for name, body := range entries {
		e, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestUnzipWritesNestedEntries(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "a.zip")
	writeZip(t, archive, map[string]string{
		"shaders/one.glsl": "one",
		"two.glsl":         "two",
	})

	dest := filepath.Join(dir, "out")
	if err := unzip(archive, dest); err != nil {
		t.Fatal(err)
	}

	for name, want := range map[string]string{
		filepath.Join(dest, "shaders", "one.glsl"): "one",
		filepath.Join(dest, "two.glsl"):            "two",
	} {
		got, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if string(got) != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

// An archive naming ../ paths would otherwise write anywhere on disk.
func TestUnzipRefusesToEscapeTheTarget(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "evil.zip")
	writeZip(t, archive, map[string]string{"../escaped.txt": "nope"})

	if err := unzip(archive, filepath.Join(dir, "out")); err == nil {
		t.Fatal("expected an error for an entry outside the target")
	}
	if _, err := os.Stat(filepath.Join(dir, "escaped.txt")); err == nil {
		t.Error("the entry was written outside the target directory")
	}
}

func TestFindAllMatchesByPattern(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.glsl", filepath.Join("nested", "b.glsl"), "c.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	found, err := findAll(dir, "*.glsl")
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 {
		t.Errorf("found %d, want 2: %v", len(found), found)
	}
}

// versions.json is shared with fetch-deps.ps1, so recording one component must
// leave the others alone.
func TestRecordKeepsOtherVersions(t *testing.T) {
	dir := t.TempDir()
	m := New(dir, nil)

	if err := m.record("rqbit", "9.0.0"); err != nil {
		t.Fatal(err)
	}
	if err := m.record("mpv", "20260814"); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "versions.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"rqbit": "9.0.0"`, `"mpv": "20260814"`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("versions.json missing %s:\n%s", want, raw)
		}
	}
}
