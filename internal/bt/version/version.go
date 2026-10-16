// Package version is how kuro names itself to peers, trackers and routers.
package version

var (
	// DefaultExtendedHandshakeClientVersion is the client name peers see in the extension handshake.
	DefaultExtendedHandshakeClientVersion = "kuro"
	// DefaultBep20Prefix starts kuro's peer IDs; bump it when kuro changes in a way other peers could care about.
	DefaultBep20Prefix = "-KU0001-"
	// DefaultHttpUserAgent is sent to HTTP trackers and web seeds.
	DefaultHttpUserAgent = "kuro"
	// DefaultUpnpId names kuro's port mapping in the router's list.
	DefaultUpnpId = "kuro"
)
