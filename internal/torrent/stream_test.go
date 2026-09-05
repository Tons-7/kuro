package torrent

import (
	"context"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Big Buck Bunny, Creative Commons Attribution 3.0. A real swarm is the only
// way to prove the streaming path — sparse reads and range serving can't be faked.
const ccMagnet = "magnet:?xt=urn:btih:dd8255ecdc7ca55fb0bbf81323d87062db1f6d1c&dn=Big+Buck+Bunny"

func ccMagnetWithTrackers() string {
	trackers := []string{
		"udp://tracker.opentrackr.org:1337/announce",
		"udp://open.stealth.si:80/announce",
		"udp://exodus.desync.com:6969/announce",
	}
	var b strings.Builder
	b.WriteString(ccMagnet)
	for _, tr := range trackers {
		b.WriteString("&tr=" + url.QueryEscape(tr))
	}
	return b.String()
}

// Guarded because it needs the sidecar and a live swarm.
func liveSupervisor(t *testing.T) *Client {
	t.Helper()
	if os.Getenv("KURO_LIVE") == "" {
		t.Skip("set KURO_LIVE=1 to run swarm-dependent tests")
	}

	root := os.Getenv("KURO_BIN")
	if root == "" {
		root = filepath.Join("..", "..", "bin")
	}
	binary := filepath.Join(root, "rqbit"+exeSuffix())
	if _, err := os.Stat(binary); err != nil {
		t.Skipf("%s not present", binary)
	}

	sup := NewSupervisor(Options{
		Binary:   binary,
		CacheDir: t.TempDir(),
		APIAddr:  "127.0.0.1:3041",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(sup.Stop)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	client, err := sup.Start(ctx)
	if err != nil {
		t.Fatalf("start rqbit: %v", err)
	}
	return client
}

func exeSuffix() string {
	if os.PathSeparator == '\\' {
		return ".exe"
	}
	return ""
}

func TestStreamFromSwarm(t *testing.T) {
	client := liveSupervisor(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	inspected, err := client.Inspect(ctx, ccMagnetWithTrackers())
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}

	file, index, ok := PickVideo(inspected.Details.Files)
	if !ok {
		t.Fatalf("no video in %q", inspected.Details.Name)
	}
	t.Logf("selected %q (%d bytes) at index %d", file.Name, file.Length, index)

	added, err := client.Add(ctx, ccMagnetWithTrackers(), file)
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	// Streaming before the engine reports live fails with an opaque 500.
	if err := client.WaitLive(ctx, added.ID, 90*time.Second); err != nil {
		t.Fatalf("wait live: %v", err)
	}

	start := time.Now()
	if err := client.Prewarm(ctx, added.ID, index, 1<<20); err != nil {
		t.Fatalf("prewarm: %v", err)
	}
	t.Logf("prewarmed head and tail in %s", time.Since(start).Round(time.Millisecond))

	stats, err := client.Stats(ctx, added.ID)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.TotalBytes == 0 {
		t.Fatal("no total size reported")
	}
	t.Logf("state=%s %.1f%% of %d MB", stats.State, stats.Percent(), stats.TotalBytes>>20)

	if err := client.Delete(ctx, added.ID); err != nil {
		t.Logf("cleanup: %v", err)
	}
}
