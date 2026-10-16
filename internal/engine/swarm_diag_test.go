package engine

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Real-swarm diagnosis: reads as ffmpeg does and logs each peer's state when a read blocks. SWARMDIAG=magnet.
func TestSwarmDiagnosis(t *testing.T) {
	magnet := os.Getenv("SWARMDIAG")
	if magnet == "" {
		t.Skip("set SWARMDIAG")
	}
	minutes, _ := strconv.Atoi(os.Getenv("SWARMMINUTES"))
	if minutes <= 0 {
		minutes = 6
	}
	out := os.Getenv("SWARMOUT")
	if out == "" {
		out = t.TempDir()
	}
	port, _ := strconv.Atoi(os.Getenv("SWARMPORT"))
	if port == 0 {
		port = 4498
	}
	dir := t.TempDir()
	e, err := New(Options{CacheDir: filepath.Join(dir, "cache"), SessionDir: filepath.Join(dir, "session"),
		ListenPort: port, UploadLimit: 4 << 20}, slog.New(slog.NewTextHandler(testLog{t}, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer e.Stop()
	c := e.Client()
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(minutes+3)*time.Minute)
	defer cancel()

	in, err := c.Inspect(ctx, magnet)
	if err != nil {
		t.Fatal(err)
	}
	file := in.Details.Files[0]
	added, err := c.Add(ctx, magnet, file)
	if err != nil {
		t.Fatal(err)
	}
	en, _ := e.entryByID(added.ID)
	tor := en.t
	pieceLen := tor.Info().PieceLength
	size := tor.Info().TotalLength()
	// ffmpeg reads at twice the video's rate; a 24-minute episode of this size gives the rate.
	rate := float64(size) / (24 * 60) * 2
	t.Logf("%s: %d MiB, pieces %d KiB, reading at %.1f MiB/s", tor.Name(), size>>20, pieceLen>>10, rate/(1<<20))

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.StreamURL(added.ID, 0), nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	var pos int64
	start := time.Now()
	end := start.Add(time.Duration(minutes) * time.Minute)
	buf := make([]byte, 256<<10)
	lastReport := time.Time{}
	stalls, dumps := 0, 0
	var blockedTotal time.Duration
	reqq := regexp.MustCompile(`reqq: (\d+)\+`)
	for time.Now().Before(end) && pos < size {
		// Paced like ffmpeg's -readrate: never further ahead of the clock than the rate allows.
		if ahead := time.Duration(float64(pos)/rate*float64(time.Second)) - time.Since(start); ahead > 0 {
			time.Sleep(ahead)
		}
		readStart := time.Now()
		done := make(chan struct{})
		var n int
		var rerr error
		go func() { n, rerr = res.Body.Read(buf); close(done) }()
		blocked := false
		for {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				if !blocked {
					blocked = true
					stalls++
				}
				piece := int(pos / pieceLen)
				st := tor.Stats()
				t.Logf("BLOCKED %4.0fs at %d MiB (piece %d, complete=%v), torrent %3.0f%% done, peers %d (seeders %d) half-open %d, useful %d MiB",
					time.Since(readStart).Seconds(), pos>>20, piece, tor.Piece(piece).State().Complete,
					100*float64(tor.BytesCompleted())/float64(size), st.ActivePeers, st.ConnectedSeeders, st.HalfOpenPeers,
					st.ConnStats.BytesReadUsefulData.Int64()>>20)
				if dumps < 6 && time.Since(readStart) > 10*time.Second {
					dumps++
					var status bytes.Buffer
					e.cl.WriteStatus(&status)
					name := filepath.Join(out, fmt.Sprintf("stall-%02d.txt", dumps))
					os.WriteFile(name, status.Bytes(), 0o644)
					// Each peer's queue and last useful chunk, on one line.
					for block := range strings.SplitSeq(status.String(), "\n\n") {
						if m := reqq.FindStringSubmatch(block); m != nil {
							t.Logf("  peer: %s", summarise(block))
						}
					}
				}
				continue
			}
			break
		}
		if blocked {
			blockedTotal += time.Since(readStart)
			t.Logf("  resumed after %.0fs", time.Since(readStart).Seconds())
		}
		pos += int64(n)
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			t.Fatalf("read at %d: %v", pos, rerr)
		}
		if time.Since(lastReport) > 30*time.Second {
			lastReport = time.Now()
			st := tor.Stats()
			t.Logf("%4.0fs: read %d MiB, torrent %3.0f%% done, peers %d (seeders %d) half-open %d known %d",
				time.Since(start).Seconds(), pos>>20, 100*float64(tor.BytesCompleted())/float64(size),
				st.ActivePeers, st.ConnectedSeeders, st.HalfOpenPeers, st.TotalPeers+st.PendingPeers)
		}
	}
	t.Logf("done: read %d of %d MiB in %.0fs, %d stall(s), %.0fs blocked in all; dumps in %s",
		pos>>20, size>>20, time.Since(start).Seconds(), stalls, blockedTotal.Seconds(), out)
}

// summarise keeps a peer block's address, request queue, flags and how long since it last helped.
func summarise(block string) string {
	var parts []string
	for line := range strings.SplitSeq(block, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "reqq:"), strings.HasPrefix(line, "last msg:"), strings.Contains(line, "dr:"):
			parts = append(parts, line)
		case len(parts) == 0 && line != "" && !strings.HasPrefix(line, "peer id"):
			parts = append(parts, line)
		}
	}
	return strings.Join(parts, " | ")
}

type testLog struct{ t *testing.T }

func (w testLog) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimSpace(string(p)))
	return len(p), nil
}
