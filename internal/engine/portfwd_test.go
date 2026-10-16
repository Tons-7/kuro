package engine

import (
	"errors"
	"log/slog"
	"testing"
)

// Port forwarding is said either way, from the mapping's own result: a router answering is not one forwarding.
func TestPortForwardingIsSaid(t *testing.T) {
	lines := &lineLog{}
	e, err := New(Options{CacheDir: t.TempDir(), SessionDir: t.TempDir()}, slog.New(lines))
	if err != nil {
		t.Fatal(err)
	}
	cfg := e.clientConfig(nil, 4240)
	cfg.UpnpDiscovered(1)
	cfg.UpnpMapped("TCP", 4240, 4240, nil)
	cfg.UpnpMapped("UDP", 4240, 0, errors.New("ConflictInMappingEntry"))
	cfg.UpnpDiscovered(0)
	for msg, want := range map[string]int{
		"the router forwards the peer port":                                                           1,
		"the router refused to forward the peer port":                                                 1,
		"no router answered for port forwarding; peers reach kuro only if the peer port is forwarded": 1,
	} {
		if got := lines.count(msg); got != want {
			t.Errorf("%q said %d times; want %d", msg, got, want)
		}
	}
}
