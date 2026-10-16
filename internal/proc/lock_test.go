package proc

import (
	"path/filepath"
	"testing"
)

// One holder at a time; the lock is free again once released.
func TestLockIsExclusiveUntilReleased(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kuro.lock")
	release, err := Lock(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Lock(path); err == nil {
		t.Fatal("a second lock on the same file was granted")
	}
	release()
	again, err := Lock(path)
	if err != nil {
		t.Fatalf("after release: %v", err)
	}
	again()
}
