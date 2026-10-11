package engine

import (
	"time"

	atorrent "github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
)

// Vars so tests need not wait.
var (
	// How long a download wanting data may have no peer before the ones it had are tried again.
	regroupAfter = 20 * time.Second
	// Per torrent, so a busy swarm cannot grow the list without end.
	rememberedPeers = 200
)

// remember notes the peers a torrent knows now. Caller holds e.mu.
func (e *Engine) remember(hash metainfo.Hash, swarm []atorrent.PeerInfo) {
	if len(swarm) == 0 {
		return
	}
	known := e.known[hash]
	if known == nil {
		known = map[string]atorrent.PeerInfo{}
		e.known[hash] = known
	}
	for _, p := range swarm {
		if p.Addr == nil || len(known) >= rememberedPeers {
			continue
		}
		// As offered, not as trusted or connected: it is only an address to try.
		known[p.Addr.String()] = atorrent.PeerInfo{Addr: p.Addr, Source: p.Source}
	}
}

// regroup tries a torrent's remembered peers again when it wants data and has nobody: an outage drops
// every peer, anacrolix forgets the ones it cannot reach, and trackers hand them out again only half-hourly.
func (e *Engine) regroup(en *entry, now time.Time) {
	t := en.t
	if t.Info() == nil {
		return
	}
	hash := t.InfoHash()
	swarm := t.KnownSwarm()
	alone := t.Stats().ActivePeers == 0

	e.mu.Lock()
	e.remember(hash, swarm)
	starved := alone && !en.paused && !en.verifying && wantsData(en)
	if !starved {
		en.aloneSince = time.Time{}
		e.mu.Unlock()
		return
	}
	if en.aloneSince.IsZero() {
		en.aloneSince = now
	}
	due := now.Sub(en.aloneSince) >= regroupAfter
	var again []atorrent.PeerInfo
	if due {
		en.aloneSince = now
		for _, p := range e.known[hash] {
			again = append(again, p)
		}
	}
	e.mu.Unlock()

	if !due {
		return
	}
	e.log.Info("download has no peers; trying the ones it had again", "name", t.Name(), "peers", len(again))
	t.AddPeers(again)
	// And asking around afresh, where that is on.
	for _, dht := range e.cl.DhtServers() {
		if _, stop, err := t.AnnounceToDht(dht); err == nil {
			time.AfterFunc(time.Minute, stop)
		}
	}
}
