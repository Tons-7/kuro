package bt

import "time"

// libtorrent's request queue: refilled at half, not once empty, it holds just RequestQueueTime of the rate.

var (
	// RequestQueueTime is how much of a peer's recent rate its queue holds: about all a new request waits.
	RequestQueueTime = 2 * time.Second
	// MinRequestQueue keeps a new or slow peer busy enough to measure.
	MinRequestQueue = 16
)

const (
	rateWindow = time.Second
	// retryEmptyAfter: an update that found nothing to add isn't rerun by every chunk sooner than this.
	retryEmptyAfter = 250 * time.Millisecond
)

// queueRate is a peer's recent useful rate, measured only while it had requests to serve.
type queueRate struct {
	windowBytes  int64
	windowExpect time.Duration
	rate         float64
	foundNothing bool
}

// sampleRate closes a window once rateWindow of expecting has passed, averaging it with the rate before.
func (p *Peer) sampleRate() {
	bytes, expect := p._stats.BytesReadUsefulData.Int64(), p.totalExpectingTime()
	q := &p.queue
	if expect-q.windowExpect < rateWindow {
		return
	}
	sample := float64(bytes-q.windowBytes) / (expect - q.windowExpect).Seconds()
	if q.rate == 0 {
		q.rate = sample
	} else {
		q.rate = (q.rate + sample) / 2
	}
	q.windowBytes, q.windowExpect = bytes, expect
}

func (cn *PeerConn) nominalMaxRequests() maxRequests {
	want := MinRequestQueue
	if r := cn.queue.rate; r > 0 && cn.t.chunkSize > 0 {
		want = max(want, int(r*RequestQueueTime.Seconds()/float64(cn.t.chunkSize)))
	}
	return max(1, min(cn.PeerMaxRequests, want, maxLocalToRemoteRequests))
}

// steals: only to a peer twice as quick, or off one silent for RequestQueueTime; "the poor" were the slow.
func (p *PeerConn) steals(existing *PeerConn, req RequestIndex, upstream bool) bool {
	// Silent since this request went out: a peer idle before it was asked hasn't failed it yet.
	sent := p.t.requestState[req].when
	if !sent.IsZero() && time.Since(sent) >= RequestQueueTime && !existing.lastUsefulChunkReceived.After(sent) {
		return true
	}
	ours, theirs := p.queue.rate, existing.queue.rate
	switch {
	case theirs <= 0:
		return upstream
	case ours <= 0:
		return false
	}
	ourWait := float64(p.requestState.Requests.GetCardinality()+1) / ours
	theirWait := float64(existing.requestState.Requests.GetCardinality()) / theirs
	return 2*ourWait < theirWait
}

// isLowOnRequests: half drained, so a refill lands before the peer runs dry.
func (p *PeerConn) isLowOnRequests() bool {
	pending := maxRequests(p.requestState.Requests.GetCardinality() + p.requestState.Cancelled.GetCardinality())
	if pending == 0 {
		return true
	}
	if pending > p.nominalMaxRequests()/2 {
		return false
	}
	return !p.queue.foundNothing || time.Since(p.lastRequestUpdate) >= retryEmptyAfter
}
