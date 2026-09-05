//go:build windows

package player

import (
	"context"
	"net"
	"time"

	"github.com/Microsoft/go-winio"
)

// mpv's IPC socket is a named pipe on Windows, which net.Dial cannot open.
func dialIPC(ctx context.Context, socket string) (net.Conn, error) {
	timeout := 2 * time.Second
	return winio.DialPipe(socket, &timeout)
}

// A named pipe: the only namespace mpv's IPC uses on Windows.
func defaultSocket() string { return `\\.\pipe\kuro-mpv` }
