package engine

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kuro/internal/bt"
)

// Two releases with one name and different content live in their own folders; deleting one leaves the
// other's bytes, and a restart keeps each where it was.
func TestSameNamedReleasesKeepTheirOwnFiles(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	miA, contentA := makePack(t, dirA)
	miB, contentB := makePack(t, dirB)
	sA, sB := seeder(t, dirA, miA), seeder(t, dirB, miB)
	peers := []bt.PeerInfo{
		{Addr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: sA.LocalPort()}, Trusted: true},
		{Addr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: sB.LocalPort()}, Trusted: true},
	}
	f := &fixture{cache: t.TempDir(), session: t.TempDir(), peer: peers[0].Addr.(*net.TCPAddr)}
	f.start(t)
	f.engine.onNew = func(tt *bt.Torrent) { tt.AddPeers(peers) }
	c := ctx(t)

	magnetA, magnetB := magnetOf(t, &miA), magnetOf(t, &miB)
	inA, err := f.client.Inspect(c, magnetA)
	if err != nil {
		t.Fatal(err)
	}
	a, err := f.client.Add(c, magnetA, inA.Details.Files[0])
	if err != nil {
		t.Fatal(err)
	}
	inB, err := f.client.Inspect(c, magnetB)
	if err != nil {
		t.Fatal(err)
	}
	b, err := f.client.Add(c, magnetB, inB.Details.Files[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []int{a.ID, b.ID} {
		if err := f.client.Await(c, id, 20*time.Second); err != nil {
			t.Fatal(err)
		}
	}
	dA, _ := f.client.Details(c, a.ID)
	dB, _ := f.client.Details(c, b.ID)
	if dA.OutputFolder == dB.OutputFolder {
		t.Fatalf("both releases landed in %s", dA.OutputFolder)
	}
	if filepath.Base(dA.OutputFolder) != "Show Pack" || !strings.HasPrefix(filepath.Base(dB.OutputFolder), "Show Pack [") {
		t.Fatalf("folders %q and %q", dA.OutputFolder, dB.OutputFolder)
	}
	gotA, _ := os.ReadFile(filepath.Join(dA.OutputFolder, "Show - 01.mkv"))
	gotB, _ := os.ReadFile(filepath.Join(dB.OutputFolder, "Show - 01.mkv"))
	if string(gotA) != string(contentA["Show - 01.mkv"]) || string(gotB) != string(contentB["Show - 01.mkv"]) {
		t.Fatal("the releases' bytes are mixed up")
	}

	if err := f.client.Delete(c, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dB.OutputFolder, "Show - 01.mkv")); err != nil {
		t.Fatal("deleting one release took the other's file")
	}
	if st, _ := f.client.Stats(c, b.ID); !st.Finished {
		t.Fatalf("the other release is no longer complete: %+v", st)
	}

	// After a restart the second release is still where it was.
	f.engine.Stop()
	f.peer = &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1}
	f.start(t)
	live, _ := f.client.Live(c)
	if len(live) != 1 {
		t.Fatalf("after restart: %v", live)
	}
	for _, id := range live {
		if err := f.client.WaitLive(c, id, 10*time.Second); err != nil {
			t.Fatal(err)
		}
		d, _ := f.client.Details(c, id)
		if d.OutputFolder != dB.OutputFolder {
			t.Fatalf("folder after restart = %q, want %q", d.OutputFolder, dB.OutputFolder)
		}
		if st, _ := f.client.Stats(c, id); !st.Finished {
			t.Fatalf("not complete after restart: %+v", st)
		}
	}
}
