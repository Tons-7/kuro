//go:build !windows

package bt

// addrTakenOS: elsewhere missinggo.IsAddrInUse already knows the system's "in use".
func addrTakenOS(error) bool { return false }
