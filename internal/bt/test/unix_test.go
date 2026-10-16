package test

import (
	"io"
	"log"
	"net"
	"path/filepath"
	"testing"

	"kuro/internal/bt"
	"kuro/internal/bt/dialer"
)

func TestUnixConns(t *testing.T) {
	var closers []io.Closer
	defer func() {
		for _, c := range closers {
			c.Close()
		}
	}()
	configure := ConfigureClient{
		Config: func(cfg *bt.ClientConfig) {
			cfg.DisableUTP = true
			cfg.DisableTCP = true
			cfg.Debug = true
		},
		Client: func(cl *bt.Client) {
			cl.AddDialer(bt.NetworkDialer{Network: "unix", Dialer: dialer.Default})
			l, err := net.Listen("unix", filepath.Join(t.TempDir(), "socket"))
			if err != nil {
				panic(err)
			}
			log.Printf("created listener %q", l)
			closers = append(closers, l)
			cl.AddListener(l)
		},
	}
	testClientTransfer(t, testClientTransferParams{
		ConfigureSeeder:  configure,
		ConfigureLeecher: configure,
	})
}
