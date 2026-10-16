package bt

import (
	"errors"
	"fmt"
	"net"
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

// A random port whose UDP twin was taken failed outright on Windows: its "in use" isn't the Unix errno.
func TestWindowsTakenPortIsRetried(t *testing.T) {
	bind := func(errno windows.Errno) error {
		return fmt.Errorf("subsequent listen: %w", &net.OpError{Op: "listen", Net: "udp4", Err: os.NewSyscallError("bind", errno)})
	}
	for _, errno := range []windows.Errno{windows.WSAEADDRINUSE, windows.WSAEACCES} {
		if !addrTakenOS(bind(errno)) {
			t.Errorf("%v is not taken as a port to retry", errno)
		}
	}
	if addrTakenOS(bind(windows.WSAENETUNREACH)) || addrTakenOS(errors.New("other")) {
		t.Error("an unrelated error is taken as a port to retry")
	}

	// The real thing: a UDP port in use refuses a second socket with the error retried.
	held, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	_, err = net.ListenPacket("udp4", held.LocalAddr().String())
	if err == nil || !addrTakenOS(err) {
		t.Errorf("binding a held port: %v; want it retried", err)
	}
}
