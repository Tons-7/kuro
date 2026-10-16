package dht

import (
	"errors"
	"syscall"

	"golang.org/x/sys/windows"
)

// Windows reports ICMP errors for earlier sends on a later UDP read; they say nothing about the socket.
func ignoreReadFromError(err error) bool {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		switch errno {
		case
			windows.WSAENETRESET,
			windows.WSAECONNRESET,
			windows.WSAECONNABORTED,
			windows.WSAECONNREFUSED,
			windows.WSAENETUNREACH,
			windows.WSAETIMEDOUT: // Why does Go have braindead syntax?
			return true
		}
	}
	return false
}
