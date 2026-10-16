package bt

import (
	"kuro/internal/bt/dialer"
)

type (
	Dialer        = dialer.T
	NetworkDialer = dialer.WithNetwork
)

var DefaultNetDialer = &dialer.Default
