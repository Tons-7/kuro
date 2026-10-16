//go:build !windows

package player

import (
	"context"
	"net"
	"os"
	"path/filepath"
)

func dialIPC(ctx context.Context, socket string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "unix", socket)
}

// A unix socket in temp, created and removed by mpv; one instance plays at a time.
func defaultSocket() string { return filepath.Join(os.TempDir(), "kuro-mpv.sock") }
