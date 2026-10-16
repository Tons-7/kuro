package engine

import (
	"sync"
	"time"

	"kuro/internal/bt"
)

// Vars so tests need not wait.
var (
	// A peer holding requests that delivered nothing useful for this long is dropped, freeing its requests.
	shedAfter = 15 * time.Second
	// A stream read waiting this long has the peers holding its piece and sending nothing dropped.
	rescueAfter = 8 * time.Second
)

// ledger follows each connection's requests and deliveries; bt calls its hooks under the client lock.
// Queue depth and request stealing are set in internal/bt/request-queue.go.
type ledger struct {
	mu    sync.Mutex
	peers map[*bt.Peer]*peerState
	now   func() time.Time
}

type peerState struct {
	sent       map[bt.Request]time.Time
	since      time.Time
	lastUseful time.Time
}

func newLedger() *ledger { return &ledger{peers: map[*bt.Peer]*peerState{}, now: time.Now} }

func (l *ledger) hook(cfg *bt.ClientConfig) {
	c := &cfg.Callbacks
	c.PeerConnAdded = append(c.PeerConnAdded, l.added)
	closed := c.PeerConnClosed
	c.PeerConnClosed = func(pc *bt.PeerConn) {
		l.closed(pc)
		if closed != nil {
			closed(pc)
		}
	}
	c.SentRequest = append(c.SentRequest, l.sentRequest)
	c.DeletedRequest = append(c.DeletedRequest, l.deletedRequest)
	c.ReceivedUsefulData = append(c.ReceivedUsefulData, l.received)
}

func (l *ledger) added(pc *bt.PeerConn) {
	now := l.now()
	l.mu.Lock()
	l.peers[&pc.Peer] = &peerState{sent: map[bt.Request]time.Time{}, since: now}
	l.mu.Unlock()
}

func (l *ledger) closed(pc *bt.PeerConn) {
	l.mu.Lock()
	delete(l.peers, &pc.Peer)
	l.mu.Unlock()
}

func (l *ledger) sentRequest(ev bt.PeerRequestEvent) {
	l.mu.Lock()
	if s := l.peers[ev.Peer]; s != nil {
		s.sent[ev.Request] = l.now()
	}
	l.mu.Unlock()
}

func (l *ledger) deletedRequest(ev bt.PeerRequestEvent) {
	l.mu.Lock()
	if s := l.peers[ev.Peer]; s != nil {
		delete(s.sent, ev.Request)
	}
	l.mu.Unlock()
}

func (l *ledger) received(ev bt.ReceivedUsefulDataEvent) {
	now := l.now()
	l.mu.Lock()
	if s := l.peers[ev.Peer]; s != nil {
		s.lastUseful = now
	}
	l.mu.Unlock()
}

// oldest is when the peer's longest-waiting request went out, zero when it holds none. Caller holds l.mu.
func (s *peerState) oldest() time.Time {
	var at time.Time
	for _, sent := range s.sent {
		if at.IsZero() || sent.Before(at) {
			at = sent
		}
	}
	return at
}

// heard is when the peer last sent something useful, or connected if it never has. Caller holds l.mu.
func (s *peerState) heard() time.Time {
	if s.lastUseful.After(s.since) {
		return s.lastUseful
	}
	return s.since
}

// silent lists the conns holding requests with nothing useful from them for shedAfter.
func (l *ledger) silent(conns []*bt.PeerConn, now time.Time) []*bt.PeerConn {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []*bt.PeerConn
	for _, pc := range conns {
		s := l.peers[&pc.Peer]
		if s == nil || len(s.sent) == 0 {
			continue
		}
		if now.Sub(s.oldest()) >= shedAfter && now.Sub(s.heard()) >= shedAfter {
			out = append(out, pc)
		}
	}
	return out
}

// holding: conns with a request in piece older than age and silent as long, and how many hold one at all.
func (l *ledger) holding(conns []*bt.PeerConn, piece int, age time.Duration, now time.Time) (stale []*bt.PeerConn, holders int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, pc := range conns {
		s := l.peers[&pc.Peer]
		if s == nil {
			continue
		}
		var held, old bool
		for r, sent := range s.sent {
			if int(r.Index) == piece {
				held = true
				old = old || now.Sub(sent) >= age
			}
		}
		if held {
			holders++
		}
		if old && now.Sub(s.heard()) >= age {
			stale = append(stale, pc)
		}
	}
	return stale, holders
}
