package library

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

	"kuro/internal/player"
	"kuro/internal/torrent"
)

// End-to-end: torrent stream over HTTP into mpv, position read back over IPC.
// Only the join of these proven parts is new, so only a real run exercises it.
// Big Buck Bunny, Creative Commons Attribution 3.0.
const testMagnet = "magnet:?xt=urn:btih:dd8255ecdc7ca55fb0bbf81323d87062db1f6d1c&dn=Big+Buck+Bunny"

func magnetWithTrackers() string {
	var b strings.Builder
	b.WriteString(testMagnet)
	for _, tr := range []string{
		"udp://tracker.opentrackr.org:1337/announce",
		"udp://open.stealth.si:80/announce",
		"udp://exodus.desync.com:6969/announce",
	} {
		b.WriteString("&tr=" + url.QueryEscape(tr))
	}
	return b.String()
}

func TestStreamIntoPlayer(t *testing.T) {
	if os.Getenv("KURO_LIVE") == "" {
		t.Skip("set KURO_LIVE=1 to run swarm-dependent tests")
	}

	bin := os.Getenv("KURO_BIN")
	if bin == "" {
		bin = filepath.Join("..", "..", "bin")
	}
	suffix := ""
	if os.PathSeparator == '\\' {
		suffix = ".exe"
	}
	rqbitPath := filepath.Join(bin, "rqbit"+suffix)
	mpvPath := filepath.Join(bin, "mpv"+suffix)

	for _, p := range []string{rqbitPath, mpvPath} {
		if _, err := os.Stat(p); err != nil {
			t.Skipf("%s not present", p)
		}
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	sup := torrent.NewSupervisor(torrent.Options{
		Binary:   rqbitPath,
		CacheDir: t.TempDir(),
		APIAddr:  "127.0.0.1:3042",
	}, log)
	defer sup.Stop()

	client, err := sup.Start(ctx)
	if err != nil {
		t.Fatalf("start torrent engine: %v", err)
	}

	magnet := magnetWithTrackers()
	inspected, err := client.Inspect(ctx, magnet)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	file, index, ok := torrent.PickVideo(inspected.Details.Files)
	if !ok {
		t.Fatal("no video file")
	}

	added, err := client.Add(ctx, magnet, file)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := client.WaitLive(ctx, added.ID, 90*time.Second); err != nil {
		t.Fatalf("wait live: %v", err)
	}
	if err := client.Prewarm(ctx, added.ID, index, 2<<20); err != nil {
		t.Fatalf("prewarm: %v", err)
	}

	streamURL := client.StreamURL(added.ID, index)
	t.Logf("streaming %s", streamURL)

	p := player.New(mpvPath, `\\.\pipe\kuro-stream-test`, log)
	defer p.Stop()

	start := time.Now()
	err = p.Play(ctx, player.Options{
		URL:   streamURL,
		Title: "kuro stream test",
		// Audio only keeps the run headless; the video path is identical.
		Extra: []string{"--no-video", "--cache=yes"},
	})
	if err != nil {
		t.Fatalf("start player: %v", err)
	}

	var firstFrame time.Duration
	var duration, reached float64

	deadline := time.After(90 * time.Second)
	for reached < 5 {
		select {
		case ev := <-p.Events():
			switch ev.Kind {
			case player.EventPosition:
				if firstFrame == 0 {
					firstFrame = time.Since(start)
				}
				reached, duration = ev.Position, ev.Duration
			case player.EventEnd, player.EventExit:
				t.Fatalf("playback stopped early at %.1fs", reached)
			}
		case <-deadline:
			t.Fatalf("never reached 5s of playback; got %.1fs", reached)
		}
	}

	t.Logf("time to first position report: %s", firstFrame.Round(time.Millisecond))
	t.Logf("played %.1fs of a %.0fs stream", reached, duration)

	if duration < 100 {
		t.Errorf("duration = %.0f, expected the full clip length", duration)
	}

	// Seeking well beyond the downloaded region is the real test: rqbit has to
	// repoint its priority window and block until those pieces arrive.
	if err := p.Seek(duration / 2); err != nil {
		t.Fatalf("seek: %v", err)
	}

	// mpv reports the seek target before data arrives, so one event past the
	// target only proves the command was accepted; resumption means it keeps advancing.
	seekStart := time.Now()
	target := duration/2 - 5
	seekDeadline := time.After(120 * time.Second)

	var arrived float64
	for {
		select {
		case ev := <-p.Events():
			if ev.Kind == player.EventExit {
				t.Fatal("player exited during seek")
			}
			if ev.Kind != player.EventPosition || ev.Position < target {
				continue
			}
			if arrived == 0 {
				arrived = ev.Position
				continue
			}
			if ev.Position > arrived+1 {
				t.Logf("playback resumed after seek in %s (%.0fs -> %.0fs)",
					time.Since(seekStart).Round(time.Millisecond), arrived, ev.Position)
				return
			}
		case <-seekDeadline:
			t.Fatalf("playback never advanced past the seek target (stuck at %.0fs)", arrived)
		}
	}
}
