package engine

import (
	"testing"
	"time"

	"kuro/internal/bt"
	pp "kuro/internal/bt/peer_protocol"
)

// fakeClock is a ledger's clock that moves only when told.
type fakeClock struct{ at time.Time }

func (c *fakeClock) now() time.Time       { return c.at }
func (c *fakeClock) pass(d time.Duration) { c.at = c.at.Add(d) }

// Dropped: holding requests with nothing useful for shedAfter. Kept: slow but still sending, or holding none.
func TestSilentPeersAreThoseHoldingRequestsWithNothingBack(t *testing.T) {
	clock := &fakeClock{at: time.Unix(1_000_000, 0)}
	l := newLedger()
	l.now = clock.now
	silent, slow, idle := &bt.PeerConn{}, &bt.PeerConn{}, &bt.PeerConn{}
	for _, pc := range []*bt.PeerConn{silent, slow, idle} {
		l.added(pc)
	}
	ask := func(pc *bt.PeerConn, piece int) {
		l.sentRequest(bt.PeerRequestEvent{Peer: &pc.Peer, Request: bt.Request{Index: pp.Integer(piece)}})
	}
	ask(silent, 1)
	ask(slow, 2)
	for range 4 {
		clock.pass(shedAfter / 3)
		l.received(bt.ReceivedUsefulDataEvent{Peer: &slow.Peer, Message: &pp.Message{Piece: make([]byte, 16<<10)}})
	}
	got := l.silent([]*bt.PeerConn{silent, slow, idle}, clock.now())
	if len(got) != 1 || got[0] != silent {
		t.Errorf("silent = %v; want only the peer that sent nothing", got)
	}
	stale, holders := l.holding([]*bt.PeerConn{silent, slow, idle}, 1, rescueAfter, clock.now())
	if holders != 1 || len(stale) != 1 || stale[0] != silent {
		t.Errorf("piece 1: %d holders, stale %v; want the silent peer alone", holders, stale)
	}
}

// A delivered or cancelled request is no longer held, so its peer is not dropped for it.
func TestAnsweredRequestsAreNotHeld(t *testing.T) {
	clock := &fakeClock{at: time.Unix(1_000_000, 0)}
	l := newLedger()
	l.now = clock.now
	pc := &bt.PeerConn{}
	l.added(pc)
	r := bt.PeerRequestEvent{Peer: &pc.Peer, Request: bt.Request{Index: 3}}
	l.sentRequest(r)
	l.deletedRequest(r)
	clock.pass(2 * shedAfter)
	if got := l.silent([]*bt.PeerConn{pc}, clock.now()); len(got) != 0 {
		t.Errorf("a peer holding nothing was found silent: %v", got)
	}
	l.closed(pc)
	if len(l.peers) != 0 {
		t.Error("a closed peer stays in the ledger")
	}
}
