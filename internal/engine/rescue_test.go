package engine

import (
	"testing"
	"time"
)

// A read stuck behind a peer that stopped sending waited 40-120 s; these run at a tenth of kuro's timings.
func silentKnobsFast() silentKnobs {
	return silentKnobs{mib: 32, pieceKiB: 256, healthyKiB: 384, slowKiB: 32, videoKiB: 96, reqq: 1024,
		observe: 20 * time.Second, fast: true}
}

// dropLimit: a read held by a quiet peer waits for the drop and a janitor round, no more.
func dropLimit() time.Duration { return shedAfter + 2*janitorEvery + time.Second }

func TestSilentPeerBesideAHealthyOneHoldsNoReadUp(t *testing.T) {
	k := silentKnobsFast()
	dir, mi, data := silentTorrent(t, k)
	r := silentStart(t, k, dir, mi, data)
	r.join(r.fake("healthy", k.healthyKiB<<10, -1))
	r.stream()
	r.awaitDeep()
	r.join(r.fake("silent", 0, 0))
	r.observe(k.observe)
	r.report()
	if took := r.longest.took; took > dropLimit() {
		t.Errorf("a read waited %s behind the silent peer; limit %s", took, dropLimit())
	}
	if r.starved.secs > int(dropLimit().Seconds()) {
		t.Errorf("reading stood still for %ds", r.starved.secs)
	}
}

// The reader's peer goes quiet holding its next chunks; a newcomer may not take them, so the quiet one must go.
func TestQuietPeerHoldingTheNextChunksIsDropped(t *testing.T) {
	k := silentKnobsFast()
	dir, mi, data := silentTorrent(t, k)
	r := silentStart(t, k, dir, mi, data)
	s := r.join(r.fake("silent", k.healthyKiB<<10, 1<<20))
	r.stream()
	r.awaitSilentHolds(s)
	r.join(r.fake("healthy", k.healthyKiB<<10, -1))
	r.observe(k.observe)
	r.report()
	if r.drops.Load() == 0 {
		t.Error("the quiet peer was never dropped")
	}
	if took := r.longest.took; took > dropLimit() {
		t.Errorf("a read waited %s on the quiet peer; limit %s", took, dropLimit())
	}
}

// Slow is not silent: a peer still delivering is never dropped as silent (holding the read's piece may be).
func TestSlowPeerIsKept(t *testing.T) {
	k := silentKnobsFast()
	dir, mi, data := silentTorrent(t, k)
	r := silentStart(t, k, dir, mi, data)
	r.join(r.fake("healthy", k.healthyKiB<<10, -1))
	r.stream()
	r.awaitDeep()
	r.join(r.fake("slow", k.slowKiB<<10, -1))
	r.observe(k.observe)
	r.report()
	if n := r.sheds.Load(); n != 0 {
		t.Errorf("%d peers dropped as silent; a slow one still sending is not", n)
	}
	if took := r.longest.took; took > dropLimit() {
		t.Errorf("a read waited %s behind the slow peer; limit %s", took, dropLimit())
	}
}
