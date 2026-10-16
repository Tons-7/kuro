package bt

import (
	"fmt"
	"io"
	"net"

	"github.com/davecgh/go-spew/spew"
	"kuro/internal/bt/dht"
	"kuro/internal/bt/dht/krpc"
	peer_store "kuro/internal/bt/dht/peer-store"
)

// DHT server interface for use by a Torrent or Client. It's reasonable for this to make assumptions
// for torrent-use that might not be the default behaviour for the DHT server.
type DhtServer interface {
	Stats() interface{}
	ID() [20]byte
	Addr() net.Addr
	AddNode(ni krpc.NodeInfo) error
	// This is called asynchronously when receiving PORT messages.
	Ping(addr *net.UDPAddr)
	Announce(hash [20]byte, port int, impliedPort bool) (DhtAnnounce, error)
	WriteStatus(io.Writer)
}

// Optional interface for DhtServer's that can expose their peer store (if any).
type PeerStorer interface {
	PeerStore() peer_store.Interface
}

type DhtAnnounce interface {
	Close()
	Peers() <-chan dht.PeersValues
}

type DhtServerWrapper struct {
	*dht.Server
}

func (me DhtServerWrapper) Stats() interface{} {
	return me.Server.Stats()
}

type dhtAnnounceWrapper struct {
	*dht.Announce
}

func (me dhtAnnounceWrapper) Peers() <-chan dht.PeersValues {
	return me.Announce.Peers
}

func (me DhtServerWrapper) Announce(hash [20]byte, port int, impliedPort bool) (DhtAnnounce, error) {
	ann, err := me.Server.Announce(hash, port, impliedPort)
	return dhtAnnounceWrapper{ann}, err
}

func (me DhtServerWrapper) Ping(addr *net.UDPAddr) {
	me.Server.PingQueryInput(addr, dht.QueryInput{
		RateLimiting: dht.QueryRateLimiting{NoWaitFirst: true},
	})
}

var _ DhtServer = DhtServerWrapper{}

func writeDhtServerStatus(w io.Writer, s DhtServer) {
	dhtStats := s.Stats()
	fmt.Fprintf(w, " ID: %x\n", s.ID())
	spew.Fdump(w, dhtStats)
}
