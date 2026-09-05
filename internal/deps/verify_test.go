package deps

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These downloads are executed, so a body that does not match the digest the
// source published must never reach bin/.
func TestDownloadRefusesAMismatchedChecksum(t *testing.T) {
	const body = "not really ffmpeg"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)

	m := New(t.TempDir(), slog.New(slog.DiscardHandler))
	dest := filepath.Join(t.TempDir(), "payload")

	sum := sha256.Sum256([]byte(body))
	real := hex.EncodeToString(sum[:])

	// The digest the source published no longer matches what arrived.
	err := m.download(context.Background(), "ffmpeg", srv.URL, dest, strings.Repeat("a", 64))
	if err == nil {
		t.Fatal("a mismatched download was accepted")
	}
	if !strings.Contains(err.Error(), "checksum") {
		t.Errorf("err = %v, want it to name the checksum", err)
	}
	if _, err := os.Stat(dest); err == nil {
		t.Error("the rejected download was left on disk")
	}

	// The real digest passes, and uppercase is still the same hash.
	if err := m.download(context.Background(), "ffmpeg", srv.URL, dest, strings.ToUpper(real)); err != nil {
		t.Fatalf("a matching download was rejected: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil || string(got) != body {
		t.Fatalf("file = %q, err = %v", got, err)
	}

	// No digest published: the install proceeds rather than being blocked.
	if err := m.download(context.Background(), "ffmpeg", srv.URL, dest, ""); err != nil {
		t.Fatalf("an unverifiable source should not block the install: %v", err)
	}
}

func TestPublishedSumReadsEitherFormat(t *testing.T) {
	hash := strings.Repeat("b", 64)
	for _, payload := range []string{hash, hash + "  ffmpeg-release-full.7z\n", "  " + hash + "\n"} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, payload)
		}))
		m := New(t.TempDir(), slog.New(slog.DiscardHandler))
		if got := m.publishedSum(context.Background(), srv.URL); got != hash {
			t.Errorf("payload %q gave %q", payload, got)
		}
		srv.Close()
	}

	// Anything that is not a hash leaves the download unverified rather than
	// failing on a moved side file.
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	t.Cleanup(bad.Close)
	m := New(t.TempDir(), slog.New(slog.DiscardHandler))
	if got := m.publishedSum(context.Background(), bad.URL); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}
