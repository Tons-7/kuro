// Package player drives mpv as a child process and talks to it over its JSON
// IPC socket, which on Windows is a named pipe.
package player

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

type EventKind string

const (
	EventPosition EventKind = "position"
	EventPause    EventKind = "pause"
	EventEnd      EventKind = "end"
	EventExit     EventKind = "exit"
)

type Event struct {
	Kind     EventKind
	Position float64
	Duration float64
	Paused   bool
	Reason   string
}

type Options struct {
	URL          string
	Title        string
	StartAt      float64
	ChaptersFile string
	SkipRanges   []SkipRange
	AutoSkip     bool
	Anime4K      bool
	Anime4KMode  string
	Anime4KSize  string
	// Audio is the sub/dub preference; mpv picks the matching track of a
	// dual-audio file by language.
	Audio string
	Extra []string
}

// AudioLanguages is mpv's --alang list for a preference, empty when either
// track will do.
func AudioLanguages(pref string) string {
	switch pref {
	case "dub":
		return "eng,en,english"
	case "sub":
		return "jpn,ja,jp,japanese"
	}
	return ""
}

type MPV struct {
	binary  string
	socket  string
	shaders string
	log     *slog.Logger

	mu     sync.Mutex
	cmd    *exec.Cmd
	conn   net.Conn
	events chan Event
	reqID  atomic.Int64

	opts     Options
	lastSkip float64
}

func New(binary, socket string, log *slog.Logger) *MPV {
	if socket == "" {
		socket = defaultSocket()
	}
	return &MPV{
		binary: binary,
		socket: socket,
		log:    log,
		events: make(chan Event, 64),
		// fetch-deps installs the Anime4K shaders beside the binary, so the
		// location needs no separate configuration.
		shaders: filepath.Join(filepath.Dir(binary), "shaders"),
	}
}

func (m *MPV) Events() <-chan Event { return m.events }

func (m *MPV) Play(ctx context.Context, opts Options) error {
	m.Stop()

	m.mu.Lock()
	m.opts = opts
	m.lastSkip = -1
	m.mu.Unlock()

	if _, err := os.Stat(m.binary); err != nil {
		return fmt.Errorf("mpv not found at %s", m.binary)
	}

	args := []string{
		"--input-ipc-server=" + m.socket,
		"--force-window=immediate",
		"--keep-open=no",
		"--title=" + orDefault(opts.Title, "kuro"),
		// The stream is served over HTTP by rqbit; without a generous cache
		// mpv re-requests aggressively and fights the piece scheduler.
		"--cache=yes",
		"--demuxer-max-bytes=256MiB",
		"--demuxer-readahead-secs=30",
	}
	if opts.StartAt > 0 {
		args = append(args, fmt.Sprintf("--start=%.3f", opts.StartAt))
	}
	if opts.ChaptersFile != "" {
		args = append(args, "--chapters-file="+opts.ChaptersFile)
	}
	if alang := AudioLanguages(opts.Audio); alang != "" {
		args = append(args, "--alang="+alang)
	}
	if opts.Anime4K {
		// gpu-next is what actually runs the shader chain well; gpu-hq only
		// raises the scaling defaults underneath it.
		args = append(args, "--profile=gpu-hq", "--vo=gpu-next")
		args = append(args, m.anime4kArgs(opts.Anime4KMode, opts.Anime4KSize)...)
	}
	args = append(args, opts.Extra...)
	args = append(args, opts.URL)

	cmd := exec.Command(m.binary, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start mpv: %w", err)
	}

	m.mu.Lock()
	m.cmd = cmd
	m.mu.Unlock()

	conn, err := m.dial(ctx, 15*time.Second)
	if err != nil {
		m.Stop()
		return err
	}

	m.mu.Lock()
	m.conn = conn
	m.mu.Unlock()

	go m.readLoop(conn)
	go m.reap(cmd)

	for i, prop := range []string{"time-pos", "duration", "pause"} {
		m.send(map[string]any{"command": []any{"observe_property", i + 1, prop}})
	}
	return nil
}

// mpv creates the pipe only after it starts, so connecting is a retry loop.
func (m *MPV) dial(ctx context.Context, timeout time.Duration) (net.Conn, error) {
	deadline := time.Now().Add(timeout)
	for {
		conn, err := dialIPC(ctx, m.socket)
		if err == nil {
			return conn, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("mpv ipc %s: %w", m.socket, err)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

type ipcMessage struct {
	Event string          `json:"event"`
	Name  string          `json:"name"`
	Data  json.RawMessage `json:"data"`
	Rsn   string          `json:"reason"`
}

func (m *MPV) readLoop(conn net.Conn) {
	scan := bufio.NewScanner(conn)
	scan.Buffer(make([]byte, 0, 8192), 1<<20)

	var duration float64
	for scan.Scan() {
		var msg ipcMessage
		if err := json.Unmarshal(scan.Bytes(), &msg); err != nil {
			continue
		}

		switch msg.Event {
		case "property-change":
			switch msg.Name {
			case "duration":
				json.Unmarshal(msg.Data, &duration)
			case "time-pos":
				var pos float64
				if json.Unmarshal(msg.Data, &pos) == nil {
					m.onPosition(pos, duration)
				}
			case "pause":
				var paused bool
				json.Unmarshal(msg.Data, &paused)
				m.emit(Event{Kind: EventPause, Paused: paused, Duration: duration})
			}
		case "end-file":
			m.emit(Event{Kind: EventEnd, Duration: duration, Reason: msg.Rsn})
		}
	}
}

func (m *MPV) onPosition(pos, duration float64) {
	m.emit(Event{Kind: EventPosition, Position: pos, Duration: duration})

	m.mu.Lock()
	opts, last := m.opts, m.lastSkip
	m.mu.Unlock()

	if !opts.AutoSkip {
		return
	}
	for _, r := range opts.SkipRanges {
		// Skipping to r.End would re-trigger on the boundary; remembering the
		// last skip keeps a seek from looping.
		if r.Contains(pos) && last != r.End {
			m.mu.Lock()
			m.lastSkip = r.End
			m.mu.Unlock()
			m.log.Info("auto-skip", "kind", r.Kind, "to", r.End)
			m.Seek(r.End)
			return
		}
	}
}

func (m *MPV) Seek(seconds float64) error {
	return m.send(map[string]any{"command": []any{"seek", seconds, "absolute"}})
}

func (m *MPV) SetPause(paused bool) error {
	return m.send(map[string]any{"command": []any{"set_property", "pause", paused}})
}

// LoadFile replaces what is playing without tearing down the window, which is
// what makes auto-advance to the next episode seamless.
func (m *MPV) LoadFile(url string) error {
	return m.send(map[string]any{"command": []any{"loadfile", url, "replace"}})
}

func (m *MPV) send(cmd map[string]any) error {
	m.mu.Lock()
	conn := m.conn
	m.mu.Unlock()

	if conn == nil {
		return fmt.Errorf("mpv not running")
	}
	cmd["request_id"] = m.reqID.Add(1)

	payload, err := json.Marshal(cmd)
	if err != nil {
		return err
	}
	_, err = conn.Write(append(payload, '\n'))
	return err
}

func (m *MPV) reap(cmd *exec.Cmd) {
	cmd.Wait()
	m.mu.Lock()
	var conn net.Conn
	if m.cmd == cmd {
		conn, m.cmd, m.conn = m.conn, nil, nil
	}
	m.mu.Unlock()
	// mpv exiting on its own never reaches Stop, and the handle would outlive
	// every episode that ends by itself.
	if conn != nil {
		conn.Close()
	}
	m.emit(Event{Kind: EventExit})
}

func (m *MPV) Running() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cmd != nil
}

func (m *MPV) Stop() {
	m.mu.Lock()
	cmd, conn := m.cmd, m.conn
	m.cmd, m.conn = nil, nil
	m.mu.Unlock()

	if conn != nil {
		conn.Close()
	}
	if cmd != nil && cmd.Process != nil {
		cmd.Process.Kill()
	}
}

// Dropping events is correct: a stalled consumer must not block playback.
func (m *MPV) emit(e Event) {
	select {
	case m.events <- e:
	default:
	}
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
