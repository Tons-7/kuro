package transcode

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Diagnostic: a source still arriving, served like the engine's stream (reads block on a hole). ARRIVE=1 to run.

// hole is the stretch [from, to) not yet downloaded; fail makes reads in it error (a paused torrent), not wait.
type hole struct {
	mu       sync.Mutex
	from, to int64
	fail     bool
	changed  chan struct{}
	readers  atomic.Int32
	requests atomic.Int32
	blocked  atomic.Int32
}

func newHole(from, to int64) *hole { return &hole{from: from, to: to, changed: make(chan struct{})} }

func (h *hole) state() (int64, int64, bool, chan struct{}) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.from, h.to, h.fail, h.changed
}

func (h *hole) set(from, to int64, fail bool) {
	h.mu.Lock()
	h.from, h.to, h.fail = from, to, fail
	close(h.changed)
	h.changed = make(chan struct{})
	h.mu.Unlock()
}

type holeyReader struct {
	f   *os.File
	h   *hole
	ctx context.Context
	pos int64
	len int64
}

func (r *holeyReader) Read(p []byte) (int, error) {
	if r.pos >= r.len {
		return 0, io.EOF
	}
	for {
		from, to, fail, ch := r.h.state()
		if r.pos >= from && r.pos < to {
			if fail {
				return 0, errors.New("torrent data downloading disabled")
			}
			r.h.blocked.Add(1)
			select {
			case <-ch:
				r.h.blocked.Add(-1)
				continue
			case <-r.ctx.Done():
				r.h.blocked.Add(-1)
				return 0, r.ctx.Err()
			}
		}
		if r.pos < from && r.pos+int64(len(p)) > from {
			p = p[:from-r.pos]
		}
		n, err := r.f.ReadAt(p, r.pos)
		r.pos += int64(n)
		if err == io.EOF && n > 0 {
			err = nil
		}
		return n, err
	}
}

func (r *holeyReader) Seek(off int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
		r.pos = off
	case io.SeekCurrent:
		r.pos += off
	case io.SeekEnd:
		r.pos = r.len + off
	}
	return r.pos, nil
}

// engineLike serves path as the engine does: http.ServeContent over a reader that blocks in the hole.
func engineLike(t *testing.T, path string, h *hole) string {
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f, err := os.Open(path)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		defer f.Close()
		h.readers.Add(1)
		h.requests.Add(1)
		defer h.readers.Add(-1)
		w.Header().Set("Content-Type", "video/x-matroska")
		http.ServeContent(w, r, "ep.mkv", time.Time{}, &holeyReader{f: f, h: h, ctx: r.Context(), len: info.Size()})
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/torrents/1/stream/0"
}

// arrivingClip is a Matroska remux like a CR WEB-DL: 8-bit H.264 at a steady rate, 2 s GOPs, AAC, an ASS track.
func arrivingClip(t *testing.T, ffmpeg string, seconds int) string {
	dir := t.TempDir()
	var ass strings.Builder
	ass.WriteString("[Script Info]\nScriptType: v4.00+\n\n[V4+ Styles]\nFormat: Name, Fontname, Fontsize, PrimaryColour, Bold\nStyle: Default,Arial,48,&H00FFFFFF,0\n\n[Events]\nFormat: Layer, Start, End, Style, Text\n")
	for s := 1; s+3 < seconds; s += 20 {
		fmt.Fprintf(&ass, "Dialogue: 0,%s,%s,Default,line at %d\n", assTime(float64(s)), assTime(float64(s+3)), s)
	}
	subs := filepath.Join(dir, "subs.ass")
	os.WriteFile(subs, []byte(ass.String()), 0o644)
	out := filepath.Join(dir, "ep.mkv")
	cmd := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", fmt.Sprintf("testsrc2=size=640x360:rate=24:duration=%d", seconds),
		"-f", "lavfi", "-i", fmt.Sprintf("sine=frequency=440:sample_rate=48000:duration=%d", seconds),
		"-i", subs,
		"-map", "0:v", "-map", "1:a", "-map", "2:s",
		"-c:v", "libx264", "-preset", "ultrafast", "-b:v", "1500k", "-maxrate", "1500k", "-bufsize", "1500k",
		"-x264-params", "keyint=48:min-keyint=48:scenecut=0",
		"-c:a", "aac", "-c:s", "copy", out)
	if o, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build clip: %v %s", err, o)
	}
	return out
}

func assTime(s float64) string {
	h := int(s) / 3600
	m := int(s) / 60 % 60
	sec := s - float64(h*3600+m*60)
	return fmt.Sprintf("%d:%02d:%05.2f", h, m, sec)
}

// served is what the player got for one segment, read the moment it was answered.
type served struct {
	n         int
	at        time.Duration
	tries     int
	bytes     int
	vFrom     float64
	vTo       float64
	aFrom     float64
	aTo       float64
	frames    int
	headFrom  int
	headTo    int
	running   bool
	fileReady bool
}

// fetch asks like hls.js: 10 s to the first byte, then again, for up to `patience`.
func fetch(t *testing.T, s *Session, n int, start time.Time, patience time.Duration) (served, []byte, []byte, error) {
	deadline := time.Now().Add(patience)
	for tries := 1; ; tries++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		path, err := s.WaitSegment(ctx, n, 90*time.Second)
		cancel()
		if err == nil {
			seg, rerr := os.ReadFile(path)
			init, _ := os.ReadFile(s.InitPath())
			s.mu.Lock()
			hf, ht, run := s.headFrom, s.headTo, s.run != nil
			s.mu.Unlock()
			return served{n: n, at: time.Since(start), tries: tries, bytes: len(seg), headFrom: hf, headTo: ht, running: run}, init, seg, rerr
		}
		if time.Now().After(deadline) {
			return served{n: n, tries: tries}, nil, nil, err
		}
	}
}

// spans reads the packet times of a served segment: video and audio first pts to last pts+duration.
func spans(t *testing.T, ffprobe string, init, seg []byte, sv *served) {
	f := filepath.Join(t.TempDir(), fmt.Sprintf("seg%d.mp4", sv.n))
	os.WriteFile(f, append(append([]byte{}, init...), seg...), 0o644)
	out, err := exec.Command(ffprobe, "-v", "error", "-show_entries", "packet=stream_index,pts_time,duration_time",
		"-of", "csv=p=0", f).Output()
	if err != nil {
		t.Logf("ffprobe seg %d: %v", sv.n, err)
		return
	}
	sv.vFrom, sv.aFrom = math.Inf(1), math.Inf(1)
	sv.vTo, sv.aTo = math.Inf(-1), math.Inf(-1)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.Split(strings.TrimSpace(line), ",")
		if len(parts) < 3 {
			continue
		}
		pts, err1 := strconv.ParseFloat(parts[1], 64)
		dur, _ := strconv.ParseFloat(parts[2], 64)
		if err1 != nil {
			continue
		}
		if parts[0] == "0" {
			sv.frames++
			sv.vFrom, sv.vTo = math.Min(sv.vFrom, pts), math.Max(sv.vTo, pts+dur)
		} else {
			sv.aFrom, sv.aTo = math.Min(sv.aFrom, pts), math.Max(sv.aTo, pts+dur)
		}
	}
}

func arriveEnabled(t *testing.T) {
	if os.Getenv("ARRIVE") == "" {
		t.Skip("set ARRIVE=1")
	}
}

type arriveRig struct {
	s       *Session
	m       *Manager
	h       *hole
	ffprobe string
	size    int64
	start   time.Time
	results []served
}

func newArriveRig(t *testing.T, seconds int, holeAt float64, encode bool) *arriveRig {
	ffmpeg, ffprobe := binaries(t)
	clip := arrivingClip(t, ffmpeg, seconds)
	info, _ := os.Stat(clip)
	h := newHole(math.MaxInt64, math.MaxInt64)
	url := engineLike(t, clip, h)
	log := slog.New(slog.NewTextHandler(testWriter{t}, &slog.HandlerOptions{Level: slog.LevelDebug}))
	m := NewManager(ffmpeg, ffprobe, t.TempDir(), "libx264", log)
	s, err := m.Open(context.Background(), "1-1", url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close("1-1") })
	if encode {
		s.Plan.VideoCopy, s.Plan.VideoCodec = false, "libx264"
	}
	// The hole opens once probing is done, from holeAt of the file to 2 MB before its end (the Cues stay readable).
	from := int64(float64(info.Size()) * holeAt)
	h.set(from, info.Size()-2<<20, false)
	t.Logf("clip %d bytes, plan %+v, segments %d, hole [%d, %d) ~ %.0fs..", info.Size(), s.Plan, s.Segments, from, info.Size()-2<<20, holeAt*float64(seconds))
	return &arriveRig{s: s, m: m, h: h, ffprobe: ffprobe, size: info.Size(), start: time.Now()}
}

func (r *arriveRig) play(t *testing.T, from, to int, patience time.Duration) {
	for n := from; n < to; n++ {
		sv, init, seg, err := fetch(t, r.s, n, r.start, patience)
		if err != nil {
			t.Logf("seg %d: NOT SERVED after %d tries: %v (blocked readers %d, readers %d)", n, sv.tries, err, r.h.blocked.Load(), r.h.readers.Load())
			r.results = append(r.results, sv)
			return
		}
		spans(t, r.ffprobe, init, seg, &sv)
		r.results = append(r.results, sv)
		t.Logf("seg %3d at %6.1fs tries %d bytes %7d video [%8.3f %8.3f] audio [%8.3f %8.3f] frames %3d head %d..%d running %v readers %d blocked %d",
			n, sv.at.Seconds(), sv.tries, sv.bytes, sv.vFrom, sv.vTo, sv.aFrom, sv.aTo, sv.frames, sv.headFrom, sv.headTo, sv.running,
			r.h.readers.Load(), r.h.blocked.Load())
	}
}

// holes reports gaps between consecutive served segments, and any served segment shorter than its slot.
func (r *arriveRig) holes(t *testing.T) (bad int) {
	for i, sv := range r.results {
		if sv.bytes == 0 {
			continue
		}
		slot := float64(sv.n) * SegmentSeconds
		if d := sv.vTo - sv.vFrom; d < SegmentSeconds-2.5 && sv.n < r.s.Segments-1 {
			t.Logf("SHORT seg %d: video %.3f s (slot %.0f..%.0f)", sv.n, d, slot, slot+SegmentSeconds)
			bad++
		}
		if i > 0 && r.results[i-1].bytes > 0 && r.results[i-1].n == sv.n-1 {
			if gap := sv.vFrom - r.results[i-1].vTo; gap > 0.1 {
				t.Logf("HOLE before seg %d: video %.3f -> %.3f (%.3f s)", sv.n, r.results[i-1].vTo, sv.vFrom, gap)
				bad++
			}
			if gap := sv.aFrom - r.results[i-1].aTo; gap > 0.1 {
				t.Logf("HOLE before seg %d: audio %.3f -> %.3f (%.3f s)", sv.n, r.results[i-1].aTo, sv.aFrom, gap)
				bad++
			}
		}
	}
	return bad
}

// The pass blocks at the hole like an engine read; the hole fills after a while. Does everything come?
func TestArrivingBlockedThenFilled(t *testing.T) {
	arriveEnabled(t)
	for _, encode := range []bool{false, true} {
		t.Run(fmt.Sprintf("encode=%v", encode), func(t *testing.T) {
			r := newArriveRig(t, 240, 0.5, encode)
			go func() {
				time.Sleep(75 * time.Second)
				t.Logf("--- hole filled at %.1fs (blocked readers %d)", time.Since(r.start).Seconds(), r.h.blocked.Load())
				r.h.set(math.MaxInt64, math.MaxInt64, false)
			}()
			r.play(t, 0, r.s.Segments, 3*time.Minute)
			if bad := r.holes(t); bad > 0 {
				t.Errorf("%d bad segments", bad)
			}
			t.Logf("engine requests %d", r.h.requests.Load())
		})
	}
}

// Reads in the hole fail for a while (a pause: the engine ends a waiting read), then succeed again.
func TestArrivingReadFailsThenResumes(t *testing.T) {
	arriveEnabled(t)
	for _, encode := range []bool{false, true} {
		t.Run(fmt.Sprintf("encode=%v", encode), func(t *testing.T) {
			r := newArriveRig(t, 240, 0.5, encode)
			from, to, _, _ := r.h.state()
			r.h.set(from, to, true)
			go func() {
				time.Sleep(40 * time.Second)
				t.Logf("--- reads work again at %.1fs", time.Since(r.start).Seconds())
				r.h.set(math.MaxInt64, math.MaxInt64, false)
			}()
			r.play(t, 0, r.s.Segments, 3*time.Minute)
			if bad := r.holes(t); bad > 0 {
				t.Errorf("%d bad segments", bad)
			}
			// What a player re-asking for the segments around the hole would now get.
			for _, sv := range r.results {
				if sv.bytes > 0 && sv.vTo-sv.vFrom < SegmentSeconds-2.5 && sv.n < r.s.Segments-1 {
					again, init, seg, err := fetch(t, r.s, sv.n, r.start, 30*time.Second)
					if err == nil {
						spans(t, r.ffprobe, init, seg, &again)
						t.Logf("re-asked seg %d: video [%.3f %.3f] tries %d", sv.n, again.vFrom, again.vTo, again.tries)
					}
				}
			}
		})
	}
}

// Blocked at the hole, the player seeks far ahead and back; the hole fills later.
func TestArrivingSeekWhileBlocked(t *testing.T) {
	arriveEnabled(t)
	r := newArriveRig(t, 240, 0.5, false)
	r.play(t, 0, 18, time.Minute)
	t.Logf("--- seek to 30 while blocked")
	r.play(t, 30, 33, time.Minute)
	t.Logf("--- seek back to 18")
	go func() {
		time.Sleep(30 * time.Second)
		t.Logf("--- hole filled at %.1fs", time.Since(r.start).Seconds())
		r.h.set(math.MaxInt64, math.MaxInt64, false)
	}()
	r.play(t, 18, r.s.Segments, 3*time.Minute)
	if bad := r.holes(t); bad > 0 {
		t.Errorf("%d bad segments", bad)
	}
}

// starts records every "encoder started" line, to see restarts the player caused.
type starts struct {
	slog.Handler
	mu   *sync.Mutex
	from *[]int64
}

func (s starts) Handle(ctx context.Context, r slog.Record) error {
	if r.Message == "encoder started" {
		r.Attrs(func(a slog.Attr) bool {
			if a.Key == "fromSegment" {
				s.mu.Lock()
				*s.from = append(*s.from, a.Value.Int64())
				s.mu.Unlock()
			}
			return true
		})
	}
	return s.Handler.Handle(ctx, r)
}
func (s starts) WithAttrs(a []slog.Attr) slog.Handler {
	return starts{s.Handler.WithAttrs(a), s.mu, s.from}
}
func (s starts) WithGroup(n string) slog.Handler { return starts{s.Handler.WithGroup(n), s.mu, s.from} }

// A player at 1x with a 20 s buffer target, as hls.js is configured; the download frontier sits at holeAt.
func TestArrivingRealtimePlayer(t *testing.T) {
	arriveEnabled(t)
	for _, encode := range []bool{false, true} {
		t.Run(fmt.Sprintf("encode=%v", encode), func(t *testing.T) {
			ffmpeg, ffprobe := binaries(t)
			clip := arrivingClip(t, ffmpeg, 180)
			info, _ := os.Stat(clip)
			h := newHole(math.MaxInt64, math.MaxInt64)
			url := engineLike(t, clip, h)
			var mu sync.Mutex
			var from []int64
			log := slog.New(starts{slog.NewTextHandler(testWriter{t}, &slog.HandlerOptions{Level: slog.LevelDebug}), &mu, &from})
			m := NewManager(ffmpeg, ffprobe, t.TempDir(), "libx264", log)
			s, err := m.Open(context.Background(), "1-1", url)
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close("1-1")
			if encode {
				s.Plan.VideoCopy, s.Plan.VideoCodec = false, "libx264"
			}
			// Frontier at ~90 s of 180; the tail (Cues) is there, as a prewarm would leave it.
			h.set(info.Size()/2, info.Size()-2<<20, false)
			fill := 120 * time.Second
			start := time.Now()
			go func() {
				time.Sleep(fill)
				t.Logf("--- frontier filled at %.1fs", time.Since(start).Seconds())
				h.set(math.MaxInt64, math.MaxInt64, false)
			}()

			var played, buffered float64
			last := time.Now()
			tick := func() {
				now := time.Now()
				played = math.Min(played+now.Sub(last).Seconds(), buffered)
				last = now
			}
			stalled := 0.0
			for n := 0; n < s.Segments; n++ {
				for {
					tick()
					if float64(n)*SegmentSeconds-played <= 20 {
						break
					}
					time.Sleep(100 * time.Millisecond)
				}
				before := played
				t0 := time.Now()
				sv, init, seg, err := fetch(t, s, n, start, 4*time.Minute)
				tick()
				if err != nil {
					t.Fatalf("seg %d not served: %v", n, err)
				}
				spans(t, ffprobe, init, seg, &sv)
				if buffered <= played+0.01 && n > 0 {
					stalled += time.Since(t0).Seconds()
				}
				buffered = math.Max(buffered, (float64(n)+1)*SegmentSeconds)
				mu.Lock()
				restarts := len(from) - 1
				mu.Unlock()
				t.Logf("seg %3d at %6.1fs played %6.1f->%6.1f tries %d video [%8.3f %8.3f] head %d..%d restarts so far %d",
					n, time.Since(start).Seconds(), before, played, sv.tries, sv.vFrom, sv.vTo, sv.headFrom, sv.headTo, restarts)
			}
			mu.Lock()
			t.Logf("passes started at segments %v; player stalled about %.0fs", from, stalled)
			mu.Unlock()
			var gone []int
			for n := 0; n < s.Segments; n++ {
				if !fileReady(s.SegmentPath(n)) {
					gone = append(gone, n)
				}
			}
			t.Logf("segments no longer on disk: %v", gone)
		})
	}
}

// Reads fail mid-segment (a pause while the pass waits), then work again: what is served for it, then and later?
func TestArrivingCutMidSegment(t *testing.T) {
	arriveEnabled(t)
	r := newArriveRig(t, 240, 0.53, false)
	from, to, _, _ := r.h.state()
	r.h.set(from, to, true)
	r.play(t, 0, 24, time.Minute)
	t.Logf("--- reads work again")
	r.h.set(math.MaxInt64, math.MaxInt64, false)
	time.Sleep(3 * time.Second)
	r.results = nil
	r.play(t, 18, r.s.Segments, time.Minute)
	r.holes(t)
	t.Logf("on disk now: %s", strings.Join(r.s.produced(), " "))
}
