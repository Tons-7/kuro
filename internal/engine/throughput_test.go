package engine

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/time/rate"
	"kuro/internal/bt"
	"kuro/internal/bt/bencode"
	"kuro/internal/bt/metainfo"
)

// bigFile is one size-byte file of random data in pieces of pieceLen, served by a seeder at bps.
func bigFile(t *testing.T, size, pieceLen int64, bps int) (metainfo.MetaInfo, *net.TCPAddr) {
	t.Helper()
	dir := t.TempDir()
	data := make([]byte, size)
	r := rand.New(rand.NewChaCha8([32]byte{1}))
	for i := range data {
		data[i] = byte(r.Uint32())
	}
	path := filepath.Join(dir, "Show - 01.mkv")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	info := metainfo.Info{PieceLength: pieceLen}
	if err := info.BuildFromFilePath(path); err != nil {
		t.Fatal(err)
	}
	var mi metainfo.MetaInfo
	var err error
	if mi.InfoBytes, err = bencode.Marshal(info); err != nil {
		t.Fatal(err)
	}
	s := seeder(t, dir, mi, func(cfg *bt.ClientConfig) {
		cfg.UploadRateLimiter = rate.NewLimiter(rate.Limit(bps), 64<<10)
	})
	return mi, &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: s.LocalPort()}
}

// A far read (a seek, MKV Cues) waited out a peer's whole queue: 4.8-11 s with 500 queued at 2 MiB/s.
func TestFarReadIsNotQueuedBehindOtherPieces(t *testing.T) {
	const pieceLen, size, target, bps = 256 << 10, 96 << 20, 80 << 20, 2 << 20
	mi, peer := bigFile(t, size, pieceLen, bps)
	var outstanding, useful, asked, got atomic.Int64
	e, id := oneSeedEngine(t, mi, peer, nil, func(cfg *bt.ClientConfig) {
		cfg.Callbacks.SentRequest = append(cfg.Callbacks.SentRequest, func(ev bt.PeerRequestEvent) {
			outstanding.Add(1)
			if int(ev.Request.Index) == target/pieceLen {
				asked.CompareAndSwap(0, time.Now().UnixNano())
			}
		})
		cfg.Callbacks.ReceivedUsefulData = append(cfg.Callbacks.ReceivedUsefulData, func(ev bt.ReceivedUsefulDataEvent) {
			if int(ev.Message.Index) == target/pieceLen {
				got.Store(time.Now().UnixNano())
			}
		})
		cfg.Callbacks.DeletedRequest = append(cfg.Callbacks.DeletedRequest, func(bt.PeerRequestEvent) { outstanding.Add(-1) })
		cfg.Callbacks.ReceivedUsefulData = append(cfg.Callbacks.ReceivedUsefulData, func(ev bt.ReceivedUsefulDataEvent) {
			useful.Add(int64(len(ev.Message.Piece)))
		})
	})

	// The file downloads at full speed, so the peer's queue is as deep as it gets.
	time.Sleep(4 * time.Second)
	en, _ := e.entryByID(id)
	if en.t.Piece(target / pieceLen).State().Complete {
		t.Fatal("target piece already downloaded; move it further out")
	}
	queued := outstanding.Load()
	req, _ := http.NewRequest(http.MethodGet, e.Client().StreamURL(id, 0), nil)
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", target, target+16<<10-1))
	before, start := useful.Load(), time.Now()
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.Copy(io.Discard, res.Body)
	res.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	took, ahead := time.Since(start), useful.Load()-before
	since := func(ns int64) float64 { return time.Duration(ns - start.UnixNano()).Seconds() }
	t.Logf("%d queued at the read; it took %.2fs, %.1f MiB arriving first; asked at %.2fs, last chunk at %.2fs",
		queued, took.Seconds(), float64(ahead)/(1<<20), since(asked.Load()), since(got.Load()))
	if bound := int64(bps*bt.RequestQueueTime.Seconds()/(16<<10)) + int64(bt.MinRequestQueue); queued > bound {
		t.Errorf("%d requests queued to one peer; %s of its speed is %d", queued, bt.RequestQueueTime, bound)
	}
	// Asked for at once; the rest of the piece can trail by refills until it goes out whole, past the queue.
	if asked.Load() == 0 || since(asked.Load()) > 1 {
		t.Errorf("the far piece was asked for at %.2fs; want at once", since(asked.Load()))
	}
	if took > 20*time.Second {
		t.Errorf("far read took %s", took)
	}
}

// The queue must cost no speed: a peer 150 ms away still gives nearly all its rate.
func TestFarPeerKeepsItsSpeed(t *testing.T) {
	const bps = 3 << 20
	mi, peer := bigFile(t, 64<<20, 256<<10, bps)
	var useful atomic.Int64
	oneSeedEngine(t, mi, peer, delayDialer{delay: 150 * time.Millisecond}, func(cfg *bt.ClientConfig) {
		cfg.Callbacks.ReceivedUsefulData = append(cfg.Callbacks.ReceivedUsefulData, func(ev bt.ReceivedUsefulDataEvent) {
			useful.Add(int64(len(ev.Message.Piece)))
		})
	})
	time.Sleep(3 * time.Second)
	from, at := useful.Load(), time.Now()
	time.Sleep(6 * time.Second)
	got := float64(useful.Load()-from) / time.Since(at).Seconds()
	t.Logf("%.2f MiB/s from a 150 ms peer serving %.2f MiB/s", got/(1<<20), float64(bps)/(1<<20))
	if got < 0.9*bps {
		t.Errorf("a far peer gave %.2f MiB/s of %.2f", got/(1<<20), float64(bps)/(1<<20))
	}
}

// oneSeedEngine adds the file to an engine whose only peer is the seeder; dialer replaces the plain ones.
func oneSeedEngine(t *testing.T, mi metainfo.MetaInfo, peer *net.TCPAddr, dialer bt.Dialer,
	configure func(*bt.ClientConfig)) (*Engine, int) {
	t.Helper()
	e, err := New(Options{CacheDir: t.TempDir(), SessionDir: t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	e.configure = func(cfg *bt.ClientConfig) {
		quietConfig(cfg)
		configure(cfg)
		if dialer != nil {
			cfg.DialForPeerConns = false
		}
	}
	e.onNew = func(tt *bt.Torrent) { tt.AddPeers([]bt.PeerInfo{{Addr: peer, Trusted: true}}) }
	t.Cleanup(e.Stop)
	cx := ctx(t)
	if err := e.Ensure(cx); err != nil {
		t.Fatal(err)
	}
	if dialer != nil {
		e.cl.AddDialer(dialer)
	}
	in, err := e.Client().Inspect(cx, magnetOf(t, &mi))
	if err != nil {
		t.Fatal(err)
	}
	added, err := e.Client().Add(cx, magnetOf(t, &mi), in.Details.Files[0])
	if err != nil {
		t.Fatal(err)
	}
	return e, added.ID
}

// delayDialer connects over TCP and holds every byte from the peer back by delay, as a distant line would.
type delayDialer struct{ delay time.Duration }

func (d delayDialer) DialerNetwork() string { return "tcp" }

func (d delayDialer) Dial(ctx context.Context, addr string) (net.Conn, error) {
	c, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	return newDelayConn(c, d.delay), nil
}

type delayed struct {
	at   time.Time
	data []byte
	err  error
}

// delayConn reads ahead into a line that releases each read only once delay has passed since it arrived.
type delayConn struct {
	net.Conn
	line chan delayed
	rest []byte
}

func newDelayConn(c net.Conn, delay time.Duration) *delayConn {
	d := &delayConn{Conn: c, line: make(chan delayed, 4096)}
	go func() {
		for {
			buf := make([]byte, 32<<10)
			n, err := c.Read(buf)
			d.line <- delayed{time.Now().Add(delay), buf[:n], err}
			if err != nil {
				close(d.line)
				return
			}
		}
	}()
	return d
}

func (d *delayConn) Read(p []byte) (int, error) {
	if len(d.rest) == 0 {
		next, ok := <-d.line
		if !ok {
			return 0, io.EOF
		}
		time.Sleep(time.Until(next.at))
		if len(next.data) == 0 {
			return 0, next.err
		}
		d.rest = next.data
	}
	n := copy(p, d.rest)
	d.rest = d.rest[n:]
	return n, nil
}
