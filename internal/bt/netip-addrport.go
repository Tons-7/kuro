package bt

import (
	"fmt"
	"net/netip"

	"kuro/internal/bt/dht/krpc"
)

type addrPorter interface {
	AddrPort() netip.AddrPort
}

func ipv4AddrPortFromKrpcNodeAddr(na krpc.NodeAddr) (_ netip.AddrPort, err error) {
	ip4 := na.IP.To4()
	if ip4 == nil {
		err = fmt.Errorf("not an ipv4 address: %v", na.IP)
		return
	}
	addr := netip.AddrFrom4(*(*[4]byte)(ip4))
	addrPort := netip.AddrPortFrom(addr, uint16(na.Port))
	return addrPort, nil
}

func ipv6AddrPortFromKrpcNodeAddr(na krpc.NodeAddr) (_ netip.AddrPort, err error) {
	ip6 := na.IP.To16()
	if ip6 == nil {
		err = fmt.Errorf("not an ipv4 address: %v", na.IP)
		return
	}
	addr := netip.AddrFrom16(*(*[16]byte)(ip6))
	addrPort := netip.AddrPortFrom(addr, uint16(na.Port))
	return addrPort, nil
}

func addrPortFromPeerRemoteAddr(pra PeerRemoteAddr) (netip.AddrPort, error) {
	switch v := pra.(type) {
	case addrPorter:
		return v.AddrPort(), nil
	case netip.AddrPort:
		return v, nil
	default:
		return netip.ParseAddrPort(pra.String())
	}
}

func krpcNodeAddrFromAddrPort(addrPort netip.AddrPort) krpc.NodeAddr {
	return krpc.NodeAddr{
		IP:   addrPort.Addr().AsSlice(),
		Port: int(addrPort.Port()),
	}
}
