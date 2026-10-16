package engine

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	"kuro/internal/bt"
	"kuro/internal/bt/metainfo"
)

// Two torrents named alike get their own folders, the first keeping the plain name; a torrent asks once
// and is answered the same after, and the name is free again once it is forgotten.
func TestLayoutKeepsSameNamedTorrentsApart(t *testing.T) {
	l := newLayout("cache")
	a, b := metainfo.Hash{1}, metainfo.Hash{2}
	if got := l.top("Show Pack", a); got != "Show Pack" {
		t.Fatalf("first = %q", got)
	}
	if got := l.top("Show Pack", b); got != "Show Pack [02000000]" {
		t.Fatalf("second = %q", got)
	}
	if got := l.top("Show Pack", a); got != "Show Pack" {
		t.Fatalf("first again = %q", got)
	}
	l.forget(a)
	if got := l.top("Show Pack", metainfo.Hash{3}); got != "Show Pack" {
		t.Fatalf("after forget = %q", got)
	}

	// Storage follows the layout.
	info := &metainfo.Info{Name: "Show Pack", PieceLength: 4, Files: []metainfo.FileInfo{{Length: 4, Path: []string{"ep.mkv"}}}}
	fs := files{base: "cache", names: l}
	if _, err := fs.OpenTorrent(context.Background(), info, b); err != nil {
		t.Fatal(err)
	}
	if got := l.top("Show Pack", b); got != "Show Pack [02000000]" {
		t.Fatalf("storage changed the name to %q", got)
	}
	var nilLayout *layout
	if got := nilLayout.top("x", a); got != "x" {
		t.Fatalf("no layout = %q", got)
	}
}

// The peers remembered are the ones seen last; a full list drops the one seen longest ago.
func TestRememberKeepsTheLatestPeers(t *testing.T) {
	old := rememberedPeers
	rememberedPeers = 2
	t.Cleanup(func() { rememberedPeers = old })
	e := &Engine{known: map[metainfo.Hash]map[string]remembered{}}
	hash := metainfo.Hash{9}
	addr := func(port int) bt.PeerInfo {
		return bt.PeerInfo{Addr: &net.TCPAddr{IP: net.IPv4(10, 0, 0, 1), Port: port}}
	}
	now := time.Now()
	e.remember(hash, []bt.PeerInfo{addr(1)}, now)
	e.remember(hash, []bt.PeerInfo{addr(2)}, now.Add(time.Second))
	e.remember(hash, []bt.PeerInfo{addr(1)}, now.Add(2*time.Second))
	e.remember(hash, []bt.PeerInfo{addr(3)}, now.Add(3*time.Second))
	known := e.known[hash]
	if len(known) != 2 {
		t.Fatalf("%d remembered, want 2", len(known))
	}
	if _, ok := known["10.0.0.1:2"]; ok {
		t.Fatal("the peer seen longest ago was kept over a newer one")
	}
	for _, port := range []string{"10.0.0.1:1", "10.0.0.1:3"} {
		if _, ok := known[port]; !ok {
			t.Errorf("%s was dropped", port)
		}
	}
}

// A starved download tries its peers after 20 s, then waits twice as long each time, up to ten minutes;
// a peer coming back starts the clock over.
func TestRegroupBacksOff(t *testing.T) {
	en := &entry{}
	start := time.Now()
	if en.tryAgain(start) {
		t.Fatal("tried at once")
	}
	if !en.tryAgain(start.Add(regroupAfter)) {
		t.Fatal("not tried after the first wait")
	}
	if en.tryAgain(start.Add(regroupAfter + regroupAfter)) {
		t.Fatal("tried again after the same wait; the second should be twice as long")
	}
	if !en.tryAgain(start.Add(3 * regroupAfter)) {
		t.Fatal("not tried after the doubled wait")
	}
	now := start
	for range 20 {
		now = now.Add(regroupMax)
		en.tryAgain(now)
	}
	if en.regroupWait != regroupMax {
		t.Fatalf("wait = %v, want it capped at %v", en.regroupWait, regroupMax)
	}
}

func TestLayoutFilePathsFollowTheTop(t *testing.T) {
	l := newLayout(filepath.Join("c", "cache"))
	l.claim(metainfo.Hash{5}, "Show Pack [05000000]")
	if got := l.top("Show Pack", metainfo.Hash{5}); got != "Show Pack [05000000]" {
		t.Fatalf("claimed top = %q", got)
	}
}
