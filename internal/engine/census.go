package engine

import (
	"sort"
	"time"
)

// Var so tests need not wait.
var censusEvery = 30 * time.Second

// Addresses named per line; a busy swarm's line stays readable.
const censusNamed = 4

// headcount is who a download was connected to at the last look, and what changed since the last log line.
type headcount struct {
	peers        map[string]bool
	joined, left []string
	loggedAt     time.Time
}

// note takes the peers connected now and returns who joined and left since the last line, when one is due:
// something changed and censusEvery has passed. A peer that came and went in between is reported as both.
func (h *headcount) note(now time.Time, current []string) (joined, left []string, due bool) {
	seen := make(map[string]bool, len(current))
	for _, addr := range current {
		seen[addr] = true
		if !h.peers[addr] {
			h.joined = append(h.joined, addr)
		}
	}
	for addr := range h.peers {
		if !seen[addr] {
			h.left = append(h.left, addr)
		}
	}
	h.peers = seen

	if len(h.joined)+len(h.left) == 0 || now.Sub(h.loggedAt) < censusEvery {
		return nil, nil, false
	}
	joined, left = h.joined, h.left
	sort.Strings(joined)
	sort.Strings(left)
	h.joined, h.left, h.loggedAt = nil, nil, now
	return joined, left, true
}

// census logs a download's peers when they change: a slow download is either a small swarm or kuro
// losing peers it could keep, and only who came and went, beside what is known, tells the two apart.
func (e *Engine) census(en *entry, now time.Time) {
	t := en.t
	if t.Info() == nil {
		return
	}
	e.mu.Lock()
	active := !en.paused && !en.verifying && wantsData(en)
	e.mu.Unlock()
	if !active {
		return
	}

	conns := t.PeerConns()
	current := make([]string, 0, len(conns))
	for _, c := range conns {
		current = append(current, c.RemoteAddr.String())
	}

	e.mu.Lock()
	joined, left, due := en.heads.note(now, current)
	e.mu.Unlock()
	if !due {
		return
	}
	st := t.Stats()
	e.log.Info("download peers changed", "name", t.Name(),
		"connected", st.ActivePeers, "seeders", st.ConnectedSeeders,
		"known", st.TotalPeers+st.PendingPeers, "halfOpen", st.HalfOpenPeers,
		"joined", len(joined), "left", len(left),
		"joinedFrom", firstFew(joined), "leftFrom", firstFew(left))
}

func firstFew(addrs []string) []string {
	if len(addrs) > censusNamed {
		return addrs[:censusNamed]
	}
	return addrs
}
