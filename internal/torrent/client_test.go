package torrent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const inspectJSON = `{
  "id": 4,
  "details": {
    "info_hash": "8011219944fe12e9",
    "name": "[SubsPlease] Show (01-12) [1080p]",
    "output_folder": "D:\\kuro\\cache\\show",
    "files": [
      {"name": "Show - 01 [1080p].mkv", "components": ["Show - 01 [1080p].mkv"], "length": 1503238553, "included": false},
      {"name": "Show - 02 [1080p].mkv", "components": ["Show - 02 [1080p].mkv"], "length": 1502000000, "included": false},
      {"name": "cover.jpg", "components": ["cover.jpg"], "length": 204800, "included": false}
    ]
  }
}`

func testClient(t *testing.T, h http.HandlerFunc) (*Client, *recorder) {
	t.Helper()
	rec := &recorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.path = r.URL.Path
		rec.query = r.URL.Query()
		rec.ranges = append(rec.ranges, r.Header.Get("Range"))
		body, _ := io.ReadAll(r.Body)
		rec.body = string(body)
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	return NewClient(srv.URL), rec
}

type recorder struct {
	path   string
	query  url.Values
	body   string
	ranges []string
}

func TestInspectDoesNotDownload(t *testing.T) {
	c, rec := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, inspectJSON)
	})

	got, err := c.Inspect(context.Background(), "magnet:?xt=urn:btih:abc")
	if err != nil {
		t.Fatal(err)
	}
	if rec.query.Get("list_only") != "true" {
		t.Errorf("list_only = %q; inspecting must not start a download", rec.query.Get("list_only"))
	}
	if rec.body != "magnet:?xt=urn:btih:abc" {
		t.Errorf("magnet not sent as the body: %q", rec.body)
	}
	if got.ID != 4 || len(got.Details.Files) != 3 {
		t.Fatalf("got %+v", got)
	}
}

// A magnet that failed to resolve is not inspected again for a while, so play
// does not spend the timeout on the same dead release while the user retries.
func TestInspectSkipsARecentlyFailedMagnet(t *testing.T) {
	var calls int
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Error(w, "no peers", http.StatusInternalServerError)
	})

	const magnet = "magnet:?xt=urn:btih:dead"
	if _, err := c.Inspect(context.Background(), magnet); err == nil {
		t.Fatal("expected the first inspect to fail")
	}
	if _, err := c.Inspect(context.Background(), magnet); err == nil {
		t.Fatal("expected the cached failure to be returned")
	}
	if calls != 1 {
		t.Errorf("the engine was hit %d times; a failed magnet should be cached", calls)
	}

	// A different magnet is unaffected.
	if _, err := c.Inspect(context.Background(), "magnet:?xt=urn:btih:other"); err == nil {
		t.Fatal("expected the other magnet to be tried")
	}
	if calls != 2 {
		t.Errorf("a different magnet should reach the engine, calls=%d", calls)
	}

	// The mark expires.
	c.mu.Lock()
	c.badMagnets[magnet] = time.Now().Add(-time.Second)
	c.mu.Unlock()
	if _, err := c.Inspect(context.Background(), magnet); err == nil {
		t.Fatal("expected a retry after expiry to hit the engine and fail")
	}
	if calls != 3 {
		t.Errorf("an expired mark should let the magnet be retried, calls=%d", calls)
	}
}

func TestInspectCancelledByTheCallerIsNotHeldAgainstTheMagnet(t *testing.T) {
	var calls atomic.Int32
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	})

	const magnet = "magnet:?xt=urn:btih:slow"
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	if _, err := c.Inspect(ctx, magnet); err == nil {
		t.Fatal("expected the cancelled inspect to fail")
	}

	if n := calls.Load(); n != 1 {
		t.Fatalf("engine hit %d times", n)
	}
	if _, bad := c.magnetFailedRecently(magnet); bad {
		t.Fatal("a caller-cancelled inspect was recorded as a dead magnet")
	}
}

// A season batch holds a dozen episodes; only the requested one may be written
// to disk, or the cache budget is meaningless.
func TestAddRestrictsToOneFile(t *testing.T) {
	c, rec := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, inspectJSON)
	})

	file := File{Name: "Show - 01 [1080p].mkv", Length: 1503238553}
	if _, err := c.Add(context.Background(), "magnet:?xt=urn:btih:abc", file); err != nil {
		t.Fatal(err)
	}

	re := rec.query.Get("only_files_regex")
	if re == "" {
		t.Fatal("no file restriction sent")
	}
	// Brackets are regex metacharacters and appear in nearly every release
	// name, so the filter has to be escaped or it matches nothing.
	if strings.Contains(re, "[1080p]") {
		t.Errorf("regex not escaped: %q", re)
	}
	if !strings.HasPrefix(re, "^") || !strings.HasSuffix(re, "$") {
		t.Errorf("regex not anchored: %q", re)
	}
}

// Matroska keeps its seek index at the end of the file, so the tail has to be
// fetched before playback or seeking does not work.
func TestPrewarmFetchesHeadAndTail(t *testing.T) {
	c, rec := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/stats/") {
			io.WriteString(w, `{"state":"live","progress_bytes":0,"total_bytes":100}`)
			return
		}
		w.WriteHeader(http.StatusPartialContent)
		w.Write(make([]byte, 16))
	})

	if err := c.Prewarm(context.Background(), 4, 0, 1<<20); err != nil {
		t.Fatal(err)
	}

	var ranges []string
	for _, r := range rec.ranges {
		if r != "" {
			ranges = append(ranges, r)
		}
	}
	if len(ranges) != 2 {
		t.Fatalf("made %d ranged requests, want a head and a tail: %v", len(ranges), ranges)
	}
	// Order matters: the last request leaves rqbit's priority window behind
	// it, and playback starts at the head.
	if ranges[0] != "bytes=-1048576" {
		t.Errorf("first range = %q; the tail holds the seek index and must come first", ranges[0])
	}
	if ranges[1] != "bytes=0-1048575" {
		t.Errorf("second range = %q; the head must be fetched last", ranges[1])
	}
}

// Streaming before rqbit reports "live" fails with an opaque 500, so callers
// must be made to wait rather than discover it at playback time.
func TestWaitLive(t *testing.T) {
	t.Run("returns once live", func(t *testing.T) {
		var calls int
		c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			calls++
			if calls < 3 {
				io.WriteString(w, `{"state":"initializing"}`)
				return
			}
			io.WriteString(w, `{"state":"live"}`)
		})
		if err := c.WaitLive(context.Background(), 4, 5*time.Second); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("surfaces a torrent error", func(t *testing.T) {
		c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{"state":"error","error":"no peers"}`)
		})
		err := c.WaitLive(context.Background(), 4, 5*time.Second)
		if err == nil || !strings.Contains(err.Error(), "no peers") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("times out", func(t *testing.T) {
		c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{"state":"initializing"}`)
		})
		if err := c.WaitLive(context.Background(), 4, 300*time.Millisecond); err == nil {
			t.Fatal("expected a timeout")
		}
	})

	t.Run("honours cancellation", func(t *testing.T) {
		c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{"state":"initializing"}`)
		})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := c.WaitLive(ctx, 4, time.Minute); err == nil {
			t.Fatal("expected cancellation")
		}
	})
}

func TestStreamURL(t *testing.T) {
	c := NewClient("http://127.0.0.1:3030/")
	if got := c.StreamURL(4, 2); got != "http://127.0.0.1:3030/torrents/4/stream/2" {
		t.Fatalf("got %q", got)
	}
}

func TestStatsPercent(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"state":"live","progress_bytes":500,"total_bytes":1000,"finished":false}`)
	})

	got, err := c.Stats(context.Background(), 4)
	if err != nil {
		t.Fatal(err)
	}
	if got.Percent() != 50 {
		t.Fatalf("percent = %v", got.Percent())
	}
	if (Stats{}).Percent() != 0 {
		t.Error("empty stats should not divide by zero")
	}
}

func TestErrorsCarryTheServerMessage(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, "invalid magnet link")
	})

	_, err := c.Inspect(context.Background(), "nonsense")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "invalid magnet link") {
		t.Errorf("error lost the server message: %v", err)
	}
}

func TestReady(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"torrents":[]}`)
	})
	if !c.Ready(context.Background()) {
		t.Error("should report ready")
	}

	dead := NewClient("http://127.0.0.1:1")
	if dead.Ready(context.Background()) {
		t.Error("should not report ready when nothing is listening")
	}
}

func TestPickVideo(t *testing.T) {
	files := []File{
		{Name: "cover.jpg", Length: 204800},
		{Name: "Show - 01 [1080p].mkv", Length: 1503238553},
		{Name: "subs/Show - 01.ass", Length: 51200},
		{Name: "sample.mkv", Length: 10485760},
	}

	got, idx, ok := PickVideo(files)
	if !ok {
		t.Fatal("no video found")
	}
	// The sample is also an mkv; size is what distinguishes it.
	if idx != 1 || got.Name != "Show - 01 [1080p].mkv" {
		t.Fatalf("picked index %d (%q)", idx, got.Name)
	}

	if _, _, ok := PickVideo([]File{{Name: "readme.txt", Length: 100}}); ok {
		t.Error("matched a non-video file")
	}
	if _, _, ok := PickVideo(nil); ok {
		t.Error("matched something in an empty list")
	}
}

func TestLifecycleEndpoints(t *testing.T) {
	tests := []struct {
		name string
		call func(*Client) error
		want string
	}{
		{"delete", func(c *Client) error { return c.Delete(context.Background(), 4) }, "/torrents/4/delete"},
		{"forget", func(c *Client) error { return c.Forget(context.Background(), 4) }, "/torrents/4/forget"},
		{"pause", func(c *Client) error { return c.Pause(context.Background(), 4) }, "/torrents/4/pause"},
		{"start", func(c *Client) error { return c.Start(context.Background(), 4) }, "/torrents/4/start"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, rec := testClient(t, func(w http.ResponseWriter, r *http.Request) {})
			if err := tt.call(c); err != nil {
				t.Fatal(err)
			}
			if rec.path != tt.want {
				t.Errorf("path = %q, want %q", rec.path, tt.want)
			}
		})
	}
}

// Startup quiets whatever rqbit resumed, except what the caller asks to keep:
// an episode somebody started stays downloading when the setting says so.
func TestPauseUnfinishedKeepsWhatIsAskedFor(t *testing.T) {
	var paused []string
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/torrents":
			io.WriteString(w, `{"torrents":[
				{"id":1,"info_hash":"AAAA","name":"a"},
				{"id":2,"info_hash":"BBBB","name":"b"},
				{"id":3,"info_hash":"CCCC","name":"c"},
				{"id":4,"info_hash":"DDDD","name":"d"}]}`)
		case strings.HasSuffix(r.URL.Path, "/stats/v1"):
			switch {
			case strings.Contains(r.URL.Path, "/3/"):
				io.WriteString(w, `{"state":"live","finished":true}`)
			case strings.Contains(r.URL.Path, "/4/"):
				io.WriteString(w, `{"state":"paused","finished":false}`)
			default:
				io.WriteString(w, `{"state":"live","finished":false}`)
			}
		case strings.HasSuffix(r.URL.Path, "/pause"):
			paused = append(paused, r.URL.Path)
		}
	})

	n, kept, checking, err := c.PauseUnfinished(context.Background(), func(hash string) bool {
		return hash == "bbbb"
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || kept != 1 || checking != 0 {
		t.Errorf("paused %d kept %d checking %d, want 1, 1, 0", n, kept, checking)
	}
	if !slices.Equal(paused, []string{"/torrents/1/pause"}) {
		t.Errorf("paused %v; only the unfinished torrent nobody asked to keep", paused)
	}
}

// After a launch rqbit re-hashes finished files and reports them as
// unfinished with progress climbing from zero. Pausing one then froze a
// downloaded episode at "12%" until someone pressed resume.
func TestPauseUnfinishedLeavesCheckingTorrentsAlone(t *testing.T) {
	var paused []string
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/torrents":
			io.WriteString(w, `{"torrents":[
				{"id":1,"info_hash":"AAAA","name":"a"},
				{"id":2,"info_hash":"BBBB","name":"b"}]}`)
		case strings.HasSuffix(r.URL.Path, "/stats/v1"):
			if strings.Contains(r.URL.Path, "/1/") {
				io.WriteString(w, `{"state":"initializing","finished":false,"progress_bytes":1200,"total_bytes":10000}`)
			} else {
				io.WriteString(w, `{"state":"live","finished":false}`)
			}
		case strings.HasSuffix(r.URL.Path, "/pause"):
			paused = append(paused, r.URL.Path)
		}
	})

	n, _, checking, err := c.PauseUnfinished(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || checking != 1 {
		t.Errorf("paused %d checking %d, want 1 and 1", n, checking)
	}
	if !slices.Equal(paused, []string{"/torrents/2/pause"}) {
		t.Errorf("paused %v; the checking torrent must be left to finish", paused)
	}
	if !(Stats{State: "initializing"}).Checking() || (Stats{State: "initializing", Finished: true}).Checking() {
		t.Error("Checking should be initializing and not finished")
	}
}

func fastPoll(t *testing.T) {
	t.Helper()
	was := awaitPoll
	awaitPoll = time.Millisecond
	t.Cleanup(func() { awaitPoll = was })
}

// Await is what makes the download queue serial: without it the queue only
// serialises the search and every episode ends up downloading at once.
func TestAwaitWaitsForTheWholeFile(t *testing.T) {
	fastPoll(t)
	var calls int
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			io.WriteString(w, `{"state":"live","progress_bytes":100,"total_bytes":300}`)
			return
		}
		io.WriteString(w, `{"state":"live","progress_bytes":300,"total_bytes":300,"finished":true}`)
	})

	if err := c.Await(context.Background(), 1, time.Minute); err != nil {
		t.Fatal(err)
	}
	if calls < 3 {
		t.Errorf("returned after %d polls, before the file was there", calls)
	}
}

// A swarm with no seeders left never finishes and never errors. Waiting on it
// forever would hold up every episode behind it.
func TestAwaitGivesUpOnAStalledSwarm(t *testing.T) {
	fastPoll(t)
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"state":"live","progress_bytes":100,"total_bytes":300}`)
	})

	err := c.Await(context.Background(), 1, 5*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "stalled") {
		t.Errorf("got %v, want a stall", err)
	}
}

// Pausing from the downloads list is a decision, not a failure, so the queue
// can tell the two apart.
func TestAwaitReportsAPause(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"state":"paused","progress_bytes":100,"total_bytes":300}`)
	})

	if err := c.Await(context.Background(), 1, time.Minute); !errors.Is(err, ErrPaused) {
		t.Errorf("got %v, want ErrPaused", err)
	}
}

// The queue standing down and the startup sweep both pause torrents, so
// anything waiting for one to go live waits for a timeout it will never beat.
func TestWaitLiveResumesAPausedTorrent(t *testing.T) {
	var started int
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/start") {
			started++
			w.WriteHeader(http.StatusOK)
			return
		}
		if started > 0 {
			io.WriteString(w, `{"state":"live"}`)
			return
		}
		io.WriteString(w, `{"state":"paused"}`)
	})

	if err := c.WaitLive(context.Background(), 1, 5*time.Second); err != nil {
		t.Fatalf("a paused torrent should be resumed, not waited on: %v", err)
	}
	if started != 1 {
		t.Errorf("start called %d times, want exactly one", started)
	}
}

// Only the head, so playback starts on bytes that are already there. The tail
// carries the seek index and is worth having, but not worth a spinner.
func TestPrewarmHeadReadsOnlyTheStart(t *testing.T) {
	c, rec := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/stats/") {
			io.WriteString(w, `{"state":"live"}`)
			return
		}
		w.WriteHeader(http.StatusPartialContent)
		w.Write(make([]byte, 1024))
	})

	if err := c.PrewarmHead(context.Background(), 1, 0, 1<<20); err != nil {
		t.Fatal(err)
	}
	for _, spec := range rec.ranges {
		if strings.HasPrefix(spec, "bytes=-") {
			t.Errorf("playback waited for the tail: %q", spec)
		}
	}
	if !slices.Contains(rec.ranges, "bytes=0-1048575") {
		t.Errorf("the head was never requested: %v", rec.ranges)
	}
}

// After a restart rqbit re-checks part-downloaded files and refuses to serve a
// byte until it finishes, so a deadline sized for a slow swarm must not expire on it.
func TestWaitLiveWaitsOutAFileCheck(t *testing.T) {
	var polls int
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		polls++
		// Checking, reporting progress, for longer than the deadline allows.
		if polls < 8 {
			fmt.Fprintf(w, `{"state":"initializing","progress_bytes":%d}`, polls*1000)
			return
		}
		io.WriteString(w, `{"state":"live"}`)
	})

	if err := c.WaitLive(context.Background(), 1, 600*time.Millisecond); err != nil {
		t.Fatalf("a file check in progress should be waited out: %v", err)
	}
}

// A check that has genuinely stopped getting anywhere still has to give up.
func TestWaitLiveGivesUpOnAStalledCheck(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"state":"initializing","progress_bytes":1000}`)
	})

	if err := c.WaitLive(context.Background(), 1, 500*time.Millisecond); err == nil {
		t.Fatal("a check making no progress should time out")
	}
}

// A tracker's seeder count is routinely wrong, so the connected peers are what
// decide whether a release is worth waiting on.
func TestStatsPeers(t *testing.T) {
	var dead Stats
	if dead.Peers() != 0 {
		t.Error("a torrent with no live section has no peers")
	}

	var live Stats
	if err := json.Unmarshal([]byte(
		`{"state":"live","live":{"snapshot":{"peer_stats":{"live":7}}}}`), &live); err != nil {
		t.Fatal(err)
	}
	if live.Peers() != 7 {
		t.Errorf("got %d peers, want 7", live.Peers())
	}
}
