package engine

import (
	"net"
	"os"
	"testing"
)

// A check of carried-over data finishing after Stop wrote the session again, into a folder already let go.
func TestNoSessionIsWrittenAfterStop(t *testing.T) {
	f := &fixture{cache: t.TempDir(), session: t.TempDir(), peer: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1}}
	f.start(t)
	if err := f.engine.Ensure(ctx(t)); err != nil {
		t.Fatal(err)
	}
	f.engine.Stop()
	path := sessionFile(f.session)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("Stop wrote no session: %v", err)
	}
	os.Remove(path)
	f.engine.saveSession()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("a save after Stop wrote the session again: %v", err)
	}
}
