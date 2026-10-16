package engine

import (
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"kuro/internal/bt"
)

// While one downloads, complete torrents stay off the network and seed again after; a paused one wants nothing.
func TestCompleteTorrentsStepAsideWhileOneDownloads(t *testing.T) {
	doneDir, slowDir := t.TempDir(), t.TempDir()
	doneMI, _ := makePack(t, doneDir)
	slowMI, _ := makePack(t, slowDir)
	done := seeder(t, doneDir, doneMI)
	slow := seeder(t, slowDir, slowMI, slowSeed)
	local := func(c *bt.Client) bt.PeerInfo {
		return bt.PeerInfo{Addr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: c.LocalPort()}, Trusted: true}
	}
	e, err := New(Options{CacheDir: t.TempDir(), SessionDir: t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	e.configure = quietConfig
	e.onNew = func(tt *bt.Torrent) { tt.AddPeers([]bt.PeerInfo{local(done), local(slow)}) }
	t.Cleanup(e.Stop)
	c, cx := e.Client(), ctx(t)

	in, err := c.Inspect(cx, magnetOf(t, &doneMI))
	if err != nil {
		t.Fatal(err)
	}
	a, err := c.Add(cx, magnetOf(t, &doneMI), in.Details.Files[2])
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Await(cx, a.ID, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	in, err = c.Inspect(cx, magnetOf(t, &slowMI))
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.Add(cx, magnetOf(t, &slowMI), in.Details.Files[0])
	if err != nil {
		t.Fatal(err)
	}
	complete, _ := e.entryByID(a.ID)
	downloading, _ := e.entryByID(b.ID)
	quiet := func(en *entry) bool {
		e.mu.Lock()
		defer e.mu.Unlock()
		return en.quiet
	}
	until := func(what string, ok func() bool) {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); !ok(); time.Sleep(50 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatal(what)
			}
		}
	}

	until("the complete torrent kept seeding while another downloaded", func() bool { return quiet(complete) })
	if quiet(downloading) {
		t.Error("the downloading torrent was kept off the network")
	}

	if err := c.Pause(cx, b.ID); err != nil {
		t.Fatal(err)
	}
	until("the complete torrent did not seed again once nothing downloaded", func() bool { return !quiet(complete) })
	if !quiet(downloading) {
		t.Error("the paused torrent stays on the network")
	}
	if p := downloading.t.Files()[0].Priority(); p != bt.PiecePriorityNone {
		t.Errorf("paused file priority %v; wanting pieces keeps a torrent dialing", p)
	}

	if err := c.Start(cx, b.ID); err != nil {
		t.Fatal(err)
	}
	until("the resumed torrent stayed quiet", func() bool { return !quiet(downloading) })
	if p := downloading.t.Files()[0].Priority(); p != bt.PiecePriorityNormal {
		t.Errorf("resumed file priority %v, want normal", p)
	}
}
