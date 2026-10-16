package peer_store

import (
	"kuro/internal/bt/metainfo"

	"kuro/internal/bt/dht/krpc"
)

type InfoHash = metainfo.Hash

type Interface interface {
	AddPeer(InfoHash, krpc.NodeAddr)
	GetPeers(InfoHash) []krpc.NodeAddr
}
