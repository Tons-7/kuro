package torrent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

type Options struct {
	Binary   string
	CacheDir string
	APIAddr  string

	// UploadLimit caps upload in bytes/sec. Seeding public trackers earns
	// nothing, but starving upload entirely gets us choked by peers, so the
	// default is a middle ground rather than the smallest possible number.
	UploadLimit int

	// ListenPort is the peer port (0 = rqbit default 4240); PeerLimit is the
	// max peers per torrent (0 = rqbit default).
	ListenPort int
	PeerLimit  int

	// DisableUPnP turns off forwarding the listen port on the router. The zero
	// value keeps forwarding on, matching rqbit: without inbound peers a home
	// connection only ever sees the few reachable outbound, and that is slow.
	DisableUPnP bool
}

// Supervisor runs rqbit as a child process tied to the application lifetime.
type Supervisor struct {
	opts Options
	log  *slog.Logger

	mu       sync.Mutex
	cmd      *exec.Cmd
	exited   chan struct{}
	starting chan struct{}
	client   *Client
	probe    *Client
	up       bool
	lastTry  time.Time
}

const (
	// A failed spawn is not retried on every request.
	respawnDelay = 15 * time.Second
	probeTimeout = 2 * time.Second
	startTimeout = 20 * time.Second
)

// ErrUnavailable wraps every reason the engine cannot be reached, so callers
// can tell "no engine" from "the engine said no".
var ErrUnavailable = errors.New("torrent engine unavailable")

func NewSupervisor(opts Options, log *slog.Logger) *Supervisor {
	if opts.APIAddr == "" {
		opts.APIAddr = "127.0.0.1:3030"
	}
	if opts.UploadLimit == 0 {
		// 4 MiB/s: enough upload to stay unchoked on public swarms without the
		// stream and the download fighting over a home connection's uplink.
		opts.UploadLimit = 4 * 1024 * 1024
	}
	return &Supervisor{opts: opts, log: log}
}

// rqbitArgs builds the engine's command line. Split out so the peer settings,
// which decide download speed, can be tested without launching anything.
func (s *Supervisor) rqbitArgs(cacheDir string) []string {
	args := []string{
		"--http-api-listen-addr", s.opts.APIAddr,
		"--ratelimit-upload", fmt.Sprint(s.opts.UploadLimit),
	}
	// rqbit forwards the port by default; only say something to turn it off.
	if s.opts.DisableUPnP {
		args = append(args, "--disable-upnp-port-forward")
	}
	if s.opts.ListenPort > 0 {
		args = append(args, "--listen-port", fmt.Sprint(s.opts.ListenPort))
	}
	if s.opts.PeerLimit > 0 {
		args = append(args, "--peer-limit", fmt.Sprint(s.opts.PeerLimit))
	}
	// Lower levels flood: a progress line per torrent per second, or a line per
	// 16KB chunk on a healthy swarm. error still surfaces real failures.
	return append(args, "-v", "error", "server", "start", cacheDir)
}

func (s *Supervisor) BaseURL() string { return "http://" + s.opts.APIAddr }

// Remote reports an engine on another machine: used as found, never started.
func (s *Supervisor) Remote() bool {
	host, _, err := net.SplitHostPort(s.opts.APIAddr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && !ip.IsLoopback()
}

// Client returns the engine's API client. Calls through it start the sidecar
// on demand, so one installed later, or one that died, needs no restart.
func (s *Supervisor) Client() *Client {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client == nil {
		s.client = NewClient(s.BaseURL()).WithEngine(s)
	}
	return s.client
}

// Down marks the engine as gone, so the next call through the client checks
// again rather than failing for the rest of the session.
func (s *Supervisor) Down() {
	s.mu.Lock()
	s.up = false
	s.mu.Unlock()
}

// Ensure brings the engine up if it is not answering. It keeps its own clocks:
// a request abandoned mid-start must not kill the engine for everyone else.
func (s *Supervisor) Ensure(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	if err := s.awaitStart(ctx); err != nil {
		return err
	}
	if s.up {
		s.mu.Unlock()
		return nil
	}
	if s.probe == nil {
		s.probe = NewClient(s.BaseURL())
	}
	if s.answering() {
		s.up = true
		s.mu.Unlock()
		return nil
	}
	if s.running() {
		s.mu.Unlock()
		return fmt.Errorf("%w: rqbit is not answering yet", ErrUnavailable)
	}
	if s.Remote() {
		s.mu.Unlock()
		return fmt.Errorf("%w: no rqbit answering at %s", ErrUnavailable, s.opts.APIAddr)
	}
	if _, err := os.Stat(s.opts.Binary); err != nil {
		s.mu.Unlock()
		return fmt.Errorf("%w: rqbit is not installed (expected at %s)", ErrUnavailable, s.opts.Binary)
	}
	if !s.lastTry.IsZero() && time.Since(s.lastTry) < respawnDelay {
		s.mu.Unlock()
		return fmt.Errorf("%w: rqbit failed to start; it is retried shortly", ErrUnavailable)
	}
	s.lastTry = time.Now()

	// Spawning takes up to startTimeout; the lock is released so other callers
	// and Stop wait on the channel with their own contexts instead.
	done := make(chan struct{})
	s.starting = done
	s.mu.Unlock()

	err := s.start()
	if err != nil {
		err = fmt.Errorf("%w: %v", ErrUnavailable, err)
	}

	s.mu.Lock()
	s.starting = nil
	close(done)
	s.up = err == nil
	s.mu.Unlock()
	return err
}

// awaitStart blocks, with the lock released, while another caller is starting
// the engine. Returns with the lock held.
func (s *Supervisor) awaitStart(ctx context.Context) error {
	for s.starting != nil {
		done := s.starting
		s.mu.Unlock()
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
		s.mu.Lock()
	}
	return nil
}

func (s *Supervisor) answering() bool {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	return s.probe.Ready(ctx)
}

// running reports a process this supervisor spawned that has not exited, so a
// slow start or a busy engine is waited for rather than started again on top.
func (s *Supervisor) running() bool {
	if s.exited == nil {
		return false
	}
	select {
	case <-s.exited:
		return false
	default:
		return true
	}
}

// Start launches the sidecar and waits for its API to answer, returning the
// client everything else uses.
func (s *Supervisor) Start(ctx context.Context) (*Client, error) {
	if err := s.Ensure(ctx); err != nil {
		return nil, err
	}
	return s.Client(), nil
}

// start runs with s.starting set and the lock released; nothing else touches
// cmd or exited until it returns.
func (s *Supervisor) start() error {
	if err := os.MkdirAll(s.opts.CacheDir, 0o755); err != nil {
		return err
	}

	// cmd.Dir is set below, and a relative executable path is resolved against
	// that directory rather than the current one, so it must be absolute.
	binary, err := filepath.Abs(s.opts.Binary)
	if err != nil {
		return err
	}
	cacheDir, err := filepath.Abs(s.opts.CacheDir)
	if err != nil {
		return err
	}

	cmd := exec.Command(binary, s.rqbitArgs(cacheDir)...)
	cmd.Dir = filepath.Dir(binary)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start rqbit: %w", err)
	}
	s.log.Info("started rqbit", "pid", cmd.Process.Pid, "cache", s.opts.CacheDir)

	exited := make(chan struct{})
	go func() {
		cmd.Wait()
		close(exited)
	}()
	s.cmd, s.exited = cmd, exited

	ctx, cancel := context.WithTimeout(context.Background(), startTimeout)
	defer cancel()
	if err := waitReady(ctx, s.probe, startTimeout); err != nil {
		s.stopLocked()
		return err
	}
	return nil
}

func waitReady(ctx context.Context, c *Client, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		probe, cancel := context.WithTimeout(ctx, time.Second)
		ready := c.Ready(probe)
		cancel()
		if ready {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("rqbit did not become ready within %s", timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func (s *Supervisor) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.awaitStart(context.Background()); err != nil {
		return
	}
	s.stopLocked()
}

func (s *Supervisor) stopLocked() {
	s.up = false
	if s.cmd == nil || s.cmd.Process == nil {
		return
	}
	s.log.Info("stopping rqbit", "pid", s.cmd.Process.Pid)
	s.cmd.Process.Kill()
	<-s.exited
	s.cmd, s.exited = nil, nil
}
