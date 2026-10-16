package server

import (
	"errors"
	"net"
	"testing"
	"time"
)

// flaky is a listener whose first Accept fails the way a busy socket table does; the next ones work.
type flaky struct {
	net.Listener
	failed bool
}

func (f *flaky) Accept() (net.Conn, error) {
	if !f.failed {
		f.failed = true
		return nil, errors.New("accept: too many open files")
	}
	return f.Listener.Accept()
}

// One accept error must not end every later Accept: Serve asks again, and the loop has to be there.
func TestDualListenerKeepsAcceptingAfterAnError(t *testing.T) {
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln := DualListener(&flaky{Listener: inner}, nil)
	t.Cleanup(func() { ln.Close() })

	if _, err := ln.Accept(); err == nil {
		t.Fatal("the first error was swallowed")
	}
	conn, err := net.DialTimeout("tcp", inner.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.Write([]byte("GET / HTTP/1.0\r\n\r\n"))
	done := make(chan error, 1)
	go func() { _, err := ln.Accept(); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the accept after the error failed: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no accept after the error: the loop had stopped")
	}
}
