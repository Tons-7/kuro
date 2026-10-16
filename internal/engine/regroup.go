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
	// Each try that brings nobody waits twice as long, up to this.
	regroupMax = 10 * time.Minute
	// Per torrent, so a busy swarm cannot grow the list without end.
	rememberedPeers = 200
)

// remembered is a peer a torrent was connected to, and when it was last seen.
type remembered struct {
	info atorrent.PeerInfo
	seen time.Time
}

// connected is the torrent's peers now; KnownSwarm reads anacrolix's maps unlocked and can crash the process.
func connected(t *atorrent.Torrent) []atorrent.PeerInfo {
	conns := t.PeerConns()
	out := make([]atorrent.PeerInfo, 0, len(conns))
	for _, c := range conns {
		if c.RemoteAddr != nil {
			out = append(out, atorrent.PeerInfo{Addr: c.RemoteAddr, Source: c.Discovery})
		}
	}
	return out
}

// remember notes the torrent's peers; when full, the one seen longest ago makes way. Caller holds e.mu.
func (e *Engine) remember(hash metainfo.Hash, peers []atorrent.PeerInfo, now time.Time) {
	if len(peers) == 0 {
		return
	}
	known := e.known[hash]
	if known == nil {
		known = map[string]remembered{}
		e.known[hash] = known
	}
	for _, p := range peers {
		if p.Addr == nil {
			continue
		}
		key := p.Addr.String()
		if _, held := known[key]; !held && len(known) >= rememberedPeers {
			oldest, at := "", now
			for k, r := range known {
				if r.seen.Before(at) {
					oldest, at = k, r.seen
				}
			}
			if oldest == "" {
				continue
			}
			delete(known, oldest)
		}
		// As an address to try, not as trusted.
		known[key] = remembered{info: atorrent.PeerInfo{Addr: p.Addr, Source: p.Source}, seen: now}
	}
}

// tryAgain: due for another try? Each wait is twice the last, from regroupAfter. Caller holds e.mu.
func (en *entry) tryAgain(now time.Time) bool {
	if en.aloneSince.IsZero() {
		en.aloneSince, en.regroupWait = now, regroupAfter
	}
	if now.Sub(en.aloneSince) < en.regroupWait {
		return false
	}
	en.aloneSince = now
	en.regroupWait = min(en.regroupWait*2, regroupMax)
	en.regroups++
	return true
}

// regroup retries a starved torrent's remembered peers (anacrolix forgets them), doubling the wait each time.
func (e *Engine) regroup(en *entry, now time.Time) {
	t := en.t
	if t.Info() == nil {
		return
	}
	hash := t.InfoHash()
	peers := connected(t)
	alone := len(peers) == 0 && t.Stats().ActivePeers == 0

	e.mu.Lock()
	e.remember(hash, peers, now)
	starved := alone && !en.paused && !en.verifying && wantsData(en)
	if !starved {
		en.aloneSince, en.regroupWait, en.regroups = time.Time{}, 0, 0
		e.mu.Unlock()
		return
	}
	tries := en.regroups
	due := en.tryAgain(now)
	var again []atorrent.PeerInfo
	if due {
		for _, r := range e.known[hash] {
			again = append(again, r.info)
		}
	}
	e.mu.Unlock()

	if !due {
		return
	}
	// The first try is news; the rest are the same line again.
	level := e.log.Info
	if tries > 0 {
		level = e.log.Debug
	}
	level("download has no peers; trying the ones it had again", "name", t.Name(), "peers", len(again), "try", tries+1)
	t.AddPeers(again)
	// And asking around afresh, where that is on.
	for _, dht := range e.cl.DhtServers() {
		if _, stop, err := t.AnnounceToDht(dht); err == nil {
			time.AfterFunc(time.Minute, stop)
		}
	}
}
