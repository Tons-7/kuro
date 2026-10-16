package transcode

import (
	"os"
	"path/filepath"
	"testing"
)

// A segment being checked could not be removed: a cut tail stayed and was served as whole.
func TestCheckingASegmentDoesNotBlockItsRemoval(t *testing.T) {
	path := filepath.Join(t.TempDir(), "seg-3.m4s")
	if err := os.WriteFile(path, []byte("....moof...."), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := openShared(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := os.Remove(path); err != nil {
		t.Fatalf("removing a segment while it is checked: %v", err)
	}
}
