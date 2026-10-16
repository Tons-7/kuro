package bt

import (
	"kuro/internal/bt/dht/krpc"

	"kuro/internal/bt/peer_protocol"
	"kuro/internal/bt/tracker"
)

// Helper-type used to bulk-manage PeerInfos.
type peerInfos []PeerInfo

func (me *peerInfos) AppendFromPex(nas []krpc.NodeAddr, fs []peer_protocol.PexPeerFlags) {
	for i, na := range nas {
		var p PeerInfo
		var f peer_protocol.PexPeerFlags
		if i < len(fs) {
			f = fs[i]
		}
		p.FromPex(na, f)
		*me = append(*me, p)
	}
}

func (ret peerInfos) AppendFromTracker(ps []tracker.Peer) peerInfos {
	for _, p := range ps {
		_p := PeerInfo{
			Addr:   ipPortAddr{p.IP, p.Port},
			Source: PeerSourceTracker,
		}
		copy(_p.Id[:], p.ID)
		ret = append(ret, _p)
	}
	return ret
}
