package engine

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A truncated session.json (a crash mid-write) must not lose every download: the list is rebuilt from
// the saved torrents, paused, and the damaged file kept aside.
func TestDamagedSessionIsRebuiltFromTheSavedTorrents(t *testing.T) {
	seedDir := t.TempDir()
	mi, _ := makePack(t, seedDir)
	session := t.TempDir()
	var buf bytes.Buffer
	mi.Write(&buf)
	hash := mi.HashInfoBytes().HexString()
	os.WriteFile(filepath.Join(session, hash+".torrent"), buf.Bytes(), 0o644)
	os.WriteFile(filepath.Join(session, "session.json"), []byte(`{"torrents":[{"info_hash":"`+hash), 0o644)

	f := &fixture{cache: t.TempDir(), session: session, peer: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1}}
	f.start(t)
	c := ctx(t)
	live, err := f.client.Live(c)
	if err != nil || len(live) != 1 {
		t.Fatalf("live = %v %v, want the saved torrent back", live, err)
	}
	id := live[hash]
	// Paused until someone asks (WaitLive is such an ask), with every file selected.
	deadline := time.Now().Add(10 * time.Second)
	for {
		st, _ := f.client.Stats(c, id)
		if st.State == "paused" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("state = %s, want paused", st.State)
		}
		time.Sleep(100 * time.Millisecond)
	}
	d, _ := f.client.Details(c, id)
	for _, file := range d.Files {
		if !file.Included {
			t.Fatalf("file %s left out; every file should be selected", file.Name)
		}
	}
	if _, err := os.Stat(filepath.Join(session, "session.json.bad")); err != nil {
		t.Error("the damaged list was not kept aside")
	}
	if body, _ := os.ReadFile(filepath.Join(session, "session.json")); !bytes.Contains(body, []byte(hash)) {
		t.Error("the rebuilt list was not saved")
	}
}
