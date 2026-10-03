package library

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Every query failing against a site that is down reads as one short line per
// site, not the raw error of each query.
func TestSearchFailureReadsAsOneLinePerSite(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	client := http.Client{Timeout: 5 * time.Second}
	refused := func(q string) error {
		_, err := client.Get("http://" + addr + "/?q=" + q)
		return fmt.Errorf("nyaa: %w", err)
	}

	var batches []searchBatch
	for _, q := range []string{"Chainsaw+Man+4242", "CSM", "Chainsawman"} {
		batches = append(batches, searchBatch{err: errors.Join(refused(q), fmt.Errorf("tokyotosho: HTTP 503"))})
	}
	got := allFailed(batches)
	if got == nil {
		t.Fatal("no error")
	}
	if want := "couldn't search for releases: nyaa refused the connection; tokyotosho: HTTP 503"; got.Error() != want {
		t.Errorf("got  %q\nwant %q", got.Error(), want)
	}
	if strings.Contains(got.Error(), addr) {
		t.Error("the message carries raw URLs")
	}
	var op *net.OpError
	if !errors.As(got, &op) {
		t.Error("the underlying network error is no longer reachable")
	}
}
