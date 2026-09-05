package transcode

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Six seconds: short enough to seek responsively, long enough that a film's
// playlist stays manageable.
const (
	SegmentSeconds = 6.0

	// Restarting the encoder is expensive, so a forward seek within this many
	// segments is served by letting the existing one catch up.
	restartAfterSegments = 4

	sessionIdle = 10 * time.Minute

	// What always works, and what a hardware encoder that fails at its first
	// frame is swapped for.
	softwareEncoder = "libx264"

	// The last lines carry the cause: ffmpeg's thread-exit lines come last, and
	// the one naming the failure sits just above them.
	encoderLogLines = 10
)

var errSessionClosed = fmt.Errorf("stream session closed")

type Session struct {
	ID       string     `json:"id"`
	Source   string     `json:"-"`
	Info     *MediaInfo `json:"info"`
	Plan     Plan       `json:"plan"`
	Segments int        `json:"segments"`

	dir        string
	encoder    string
	ffmpeg     string
	onSeek     SeekHook
	onIdle     func(id string)
	onFallback func(codec string)
	log        *slog.Logger

	mu  sync.Mutex
	run *run
	// Segments requests are waiting on right now, so a restart can let the
	// pass serving them finish first (Jellyfin waits for active requests the
	// same way) instead of stranding a waiter on a killed pass.
	waiters  map[int]int
	headFrom int
	// headTo is the highest segment the current pass has finished, headFrom-1
	// before it produces any. The restart decision compares against this — where
	// the encoder actually is — not against a stale file from an earlier pass.
	headTo  int
	touched time.Time
	// Position in Info.Audio of the track being encoded, and the sub/dub
	// preference that last picked it. Reopening a live session with the same
	// preference must not re-pick: that drops every segment mid-load and hands
	// the player an init file its segments no longer match. A changed preference
	// (the Sub/Dub toggle) does re-pick.
	audioTrack int
	audioPref  string
	// Set once the session is released, so a late waiter's restart branch does
	// not relaunch ffmpeg into a directory that is being torn down.
	closed bool
	// Serialises a whole start (kill, launch, publish) so two callers cannot
	// each spawn an encoder into the same directory.
	startMu sync.Mutex
	// Opening the same episode twice returns this session, so closing it once
	// must not stop what another holder is still watching.
	refs int
	// A software failure is reported, not retried.
	fellBack bool
}

// run is one encoder process: what it was started from and when it has gone,
// so the file it was writing can be cleaned up only once it has let go of it.
type run struct {
	cmd    *exec.Cmd
	stderr strings.Builder
	from   int
	exited chan struct{}
}

// SeekHook is told where playback jumped to, as a fraction of the file, so the
// source can start fetching there.
type SeekHook func(sessionID string, fraction float64)

type Manager struct {
	ffmpeg  string
	prober  *Prober
	root    string
	encoder string
	onSeek  SeekHook
	onIdle  func(id string)
	log     *slog.Logger

	mu       sync.Mutex
	sessions map[string]*Session
}

func (m *Manager) OnSeek(hook SeekHook) { m.onSeek = hook }

// OnIdle is called for a session the reaper closed. A killed browser never
// tells the server otherwise, leaving the episode pinned and downloading.
func (m *Manager) OnIdle(hook func(id string)) { m.onIdle = hook }

func NewManager(ffmpeg, ffprobe, root, encoder string, log *slog.Logger) *Manager {
	return &Manager{
		ffmpeg:   absolute(ffmpeg),
		prober:   NewProber(absolute(ffprobe)),
		root:     root,
		encoder:  encoder,
		log:      log,
		sessions: map[string]*Session{},
	}
}

// The encoder's working directory is the segment folder, and Windows resolves
// a relative executable against that, so the path has to be absolute.
func absolute(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return abs
}

// Purge clears transcode output from a previous run. No session survives a
// restart, so anything still on disk is dead weight.
func (m *Manager) Purge() (int, error) {
	root := filepath.Join(m.root, "hls")

	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}

	var removed int
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(root, e.Name())); err != nil {
			m.log.Warn("purge transcode dir", "dir", e.Name(), "err", err)
			continue
		}
		removed++
	}
	if removed > 0 {
		m.log.Info("cleared stale transcode output", "sessions", removed)
	}
	return removed, nil
}

func (m *Manager) Open(ctx context.Context, id, source string) (*Session, error) {
	return m.OpenProbing(ctx, id, source, source)
}

// OpenProbing probes one source and encodes from another. A torrent must be
// encoded through the engine so ranged reads move the priority window, but
// probed from disk to avoid waiting on the header.
func (m *Manager) OpenProbing(ctx context.Context, id, source, probeSource string) (*Session, error) {
	m.mu.Lock()
	if s, ok := m.sessions[id]; ok && s.Source == source {
		s.touch()
		s.mu.Lock()
		s.refs++
		s.mu.Unlock()
		m.mu.Unlock()
		return s, nil
	}
	_, replacing := m.sessions[id]
	m.mu.Unlock()

	// Same episode from another release: the old encoder would run on orphaned.
	if replacing {
		m.discard(id)
	}

	info, err := m.prober.Probe(ctx, probeSource)
	if err != nil && probeSource != source {
		m.log.Warn("probe from disk failed, reading through the engine",
			"session", id, "err", err)
		info, err = m.prober.Probe(ctx, source)
	}
	if err != nil {
		return nil, err
	}

	dir := filepath.Join(m.root, "hls", id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	encoder := m.currentEncoder()
	s := &Session{
		ID: id, Source: source, Info: info,
		Plan:     PlanFor(info, encoder),
		Segments: segmentCount(info.Duration),
		dir:      dir, encoder: encoder, ffmpeg: m.ffmpeg, log: m.log,
		onSeek:     m.onSeek,
		onFallback: m.softwareOnly,
		headFrom:   -1,
		touched:    time.Now(),
		refs:       1,
	}

	m.mu.Lock()
	// Another open raced this one to the same episode; keep theirs so both
	// holders are counted against a single encoder.
	if existing, ok := m.sessions[id]; ok {
		if existing.Source == source {
			existing.mu.Lock()
			existing.refs++
			existing.mu.Unlock()
			m.mu.Unlock()
			s.stop()
			return existing, nil
		}
		// Raced by an open on yet another source; the newest wins.
		defer m.discardSession(id, existing)
	}
	m.sessions[id] = s
	m.mu.Unlock()

	m.log.Info("stream session opened", "id", id,
		"duration", int(info.Duration), "plan", s.Plan.Reason)
	return s, nil
}

func (m *Manager) currentEncoder() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.encoder
}

// SetEncoder replaces what new sessions encode with, for when ffmpeg arrives
// after launch and the detection at startup had nothing to ask.
func (m *Manager) SetEncoder(encoder string) {
	if encoder == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.encoder = encoder
}

// softwareOnly is told that a session's hardware encoder died before its first
// segment. Later sessions go straight to software rather than failing the same way.
func (m *Manager) softwareOnly(codec string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.encoder == softwareEncoder {
		return
	}
	m.log.Warn("hardware encoder abandoned for this run",
		"encoder", codec, "using", softwareEncoder)
	m.encoder = softwareEncoder
}

func (m *Manager) Get(id string) (*Session, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if ok {
		s.touch()
	}
	return s, ok
}

// Close releases one hold on a session and reports whether that was the last
// one. Anything that stops the download belongs behind a true, so the encoder
// is not killed while another holder is still watching.
func (m *Manager) Close(id string) bool {
	m.mu.Lock()
	s, ok := m.sessions[id]
	if !ok {
		m.mu.Unlock()
		return false
	}

	s.mu.Lock()
	s.refs--
	last := s.refs <= 0
	if last {
		s.closed = true
	}
	s.mu.Unlock()

	if last {
		delete(m.sessions, id)
	}
	m.mu.Unlock()

	if last {
		s.stop()
		os.RemoveAll(s.dir)
	}
	return last
}

// discard drops a session outright, however many holders it has: the reaper,
// shutdown, and a reopen on another source, where nobody watches the old one.
func (m *Manager) discard(id string) {
	m.mu.Lock()
	s, ok := m.sessions[id]
	delete(m.sessions, id)
	m.mu.Unlock()

	if ok {
		m.discardSession(id, s)
	}
}

// discardSession tears down an unlinked session; the directory is shared by id,
// so it goes only if no replacement owns it.
func (m *Manager) discardSession(id string, s *Session) {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.stop()

	m.mu.Lock()
	owner := m.sessions[id]
	m.mu.Unlock()
	if owner == nil || owner == s {
		os.RemoveAll(s.dir)
	}
}

// Sources lists what the open sessions are reading. A season pack serves
// several episodes from one torrent, so closing one is not a reason to stop
// fetching it.
func (m *Manager) Sources() []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]string, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, s.Source)
	}
	return out
}

// Reap discards sessions nobody is watching. Each one holds an ffmpeg process
// and a directory of segments.
func (m *Manager) Reap(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			m.closeAll()
			return
		case <-time.After(time.Minute):
		}

		m.mu.Lock()
		var stale []string
		for id, s := range m.sessions {
			if time.Since(s.lastTouch()) > sessionIdle {
				stale = append(stale, id)
			}
		}
		m.mu.Unlock()

		for _, id := range stale {
			m.log.Info("stream session expired", "id", id)
			m.discard(id)
			if m.onIdle != nil {
				m.onIdle(id)
			}
		}
	}
}

func (m *Manager) closeAll() {
	m.mu.Lock()
	ids := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		ids = append(ids, id)
	}
	m.mu.Unlock()

	for _, id := range ids {
		m.discard(id)
	}
}

func (s *Session) touch() {
	s.mu.Lock()
	s.touched = time.Now()
	s.mu.Unlock()
}

func (s *Session) lastTouch() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.touched
}

// segmentCount is how many segments the playlist advertises. A remainder under
// a second folds into the previous segment: the muxer cuts on keyframes and
// never writes a sliver that short on its own.
func segmentCount(duration float64) int {
	n := int(duration / SegmentSeconds)
	if duration-float64(n)*SegmentSeconds >= 1 {
		n++
	}
	return max(n, 1)
}

// Written up front for the whole duration rather than grown as segments
// appear, so the seek bar is real immediately; asking for a segment makes it.
func (s *Session) Playlist() string {
	var b strings.Builder
	fmt.Fprintf(&b, "#EXTM3U\n#EXT-X-VERSION:7\n")
	fmt.Fprintf(&b, "#EXT-X-TARGETDURATION:%d\n", int(SegmentSeconds)+1)
	fmt.Fprintf(&b, "#EXT-X-MEDIA-SEQUENCE:0\n")
	fmt.Fprintf(&b, "#EXT-X-PLAYLIST-TYPE:VOD\n")
	fmt.Fprintf(&b, "#EXT-X-MAP:URI=\"init.mp4\"\n")

	for i := 0; i < s.Segments; i++ {
		d := SegmentSeconds
		if i == s.Segments-1 {
			// The last one takes whatever is left, sliver included.
			d = max(s.Info.Duration-float64(i)*SegmentSeconds, 0.001)
		}
		fmt.Fprintf(&b, "#EXTINF:%.3f,\n%d.mp4\n", d, i)
	}
	b.WriteString("#EXT-X-ENDLIST\n")
	return b.String()
}

func (s *Session) SegmentPath(n int) string {
	return filepath.Join(s.dir, fmt.Sprintf("%d.mp4", n))
}

func (s *Session) InitPath() string { return filepath.Join(s.dir, "init.mp4") }

// Prestart launches the encoder at a segment if nothing is running and the
// segment is not already on disk, and returns at once. It never moves a running
// pass: only the player's own segment requests drive the encoder, so a head
// start at a stale resume point cannot fight the segment the player then asks for.
func (s *Session) Prestart(from int) error {
	if fileReady(s.SegmentPath(from)) {
		return nil
	}
	return s.startIfIdle(from)
}

// startIfIdle launches a pass at from only when no pass exists, deciding under
// the same lock that publishes one. A plain running() check raced: sampled
// while another start was mid-launch it read idle, and the start that followed
// killed the pass the player's own request had just begun.
func (s *Session) startIfIdle(from int) error {
	s.startMu.Lock()
	defer s.startMu.Unlock()

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errSessionClosed
	}
	if s.run != nil {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	return s.startLocked(from)
}

// WaitInit returns once the init segment exists. Every pass writes it first, so
// whichever pass is running — or one started at from if none is — produces it.
// Unlike WaitSegment this never redirects a running encoder: the init request
// arrives alongside the first segment request, and aiming both at different
// segments killed each other's pass before either wrote anything.
func (s *Session) WaitInit(ctx context.Context, from int, timeout time.Duration) (string, error) {
	path := s.InitPath()
	if fileReady(path) {
		s.touch()
		return path, nil
	}
	if err := s.startIfIdle(from); err != nil {
		return "", err
	}

	deadline := time.After(timeout)
	for {
		if fileReady(path) {
			s.touch()
			return path, nil
		}
		// The pass died before writing it; one relaunch, like WaitSegment.
		if !s.running() {
			if err := s.startIfIdle(from); err != nil {
				return "", err
			}
			if err := s.awaitFile(ctx, path, deadline); err != nil {
				return "", err
			}
			return path, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-deadline:
			return "", fmt.Errorf("init segment not ready in %s (produced: %s)",
				timeout, strings.Join(s.produced(), ", "))
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (s *Session) awaitFile(ctx context.Context, path string, deadline <-chan time.Time) error {
	for !fileReady(path) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			return fmt.Errorf("%s was not produced", filepath.Base(path))
		case <-time.After(100 * time.Millisecond):
		}
	}
	s.touch()
	return nil
}

// WaitSegment returns once a segment exists, starting or moving the encoder if
// it is not already producing it.
func (s *Session) WaitSegment(ctx context.Context, n int, timeout time.Duration) (string, error) {
	path := s.SegmentPath(n)
	if fileReady(path) {
		s.touch()
		return path, nil
	}

	// Known to restarts, so a pass about to serve this request is not killed
	// under it.
	s.addWaiter(n)
	defer s.dropWaiter(n)

	if err := s.ensureHead(n); err != nil {
		return "", err
	}

	deadline := time.After(timeout)
	var restarted bool
	for {
		s.advanceHead()

		// A segment is only known complete once the next one appears, the
		// encoder has exited, or it is the last one the playlist advertises.
		if fileReady(path) && (fileReady(s.SegmentPath(n+1)) || n >= s.Segments-1 || !s.running()) {
			s.touch()
			return path, nil
		}

		// The pass ended before this segment: stream copy cuts on keyframes, so
		// the last keyframe can fall short of the arithmetic. A pass aimed at the
		// segment writes its tail — once.
		if !s.running() && !fileReady(path) {
			if restarted {
				return "", fmt.Errorf("segment %d was not produced", n)
			}
			restarted = true
			if err := s.start(n); err != nil {
				return "", err
			}
			continue
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-deadline:
			return "", fmt.Errorf("segment %d not ready in %s (produced: %s)",
				n, timeout, strings.Join(s.produced(), ", "))
		case <-time.After(150 * time.Millisecond):
		}
	}
}

// produced lists what the encoder actually wrote, which is the only way to
// tell a dead encoder from one writing somewhere unexpected.
func (s *Session) produced() []string {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return []string{"unreadable: " + err.Error()}
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if info, err := e.Info(); err == nil {
			out = append(out, fmt.Sprintf("%s(%d)", e.Name(), info.Size()))
		}
	}
	if len(out) == 0 {
		return []string{"nothing"}
	}
	return out
}

func fileReady(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Size() > 0
}

func (s *Session) running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.run != nil
}

// CurrentPlan is the plan as it stands: a software fallback rewrites it while
// the session is live, so a reader outside this package needs the lock.
func (s *Session) CurrentPlan() Plan {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Plan
}

// Started reports whether an encoder pass is already running for this session.
func (s *Session) Started() bool { return s.running() }

// advanceHead moves headTo up to the highest segment the current pass has
// finished, so the restart decision knows where the encoder really is. A
// segment counts as done once the next one has started (the same rule
// WaitSegment returns on), so a half-written tail is not mistaken for progress.
func (s *Session) advanceHead() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.run == nil {
		return
	}
	// A segment counts once the next one has started, the same "complete" rule
	// WaitSegment returns on. Everything from the pass start upward was cleared
	// at launch, so a file here is this pass's own, not a stale one.
	for fileReady(s.SegmentPath(s.headTo+1)) && fileReady(s.SegmentPath(s.headTo+2)) {
		s.headTo++
	}
}

// ensureHead starts the encoder, or moves it, when the requested segment is
// outside what the current pass can deliver soon. The rule is Jellyfin's: a
// segment before this pass, or far past where the encoder has actually reached,
// needs a restart there; anything within reach is just waited for. An already
// produced segment (from this or an earlier pass) is served by WaitSegment
// before this is ever called.
func (s *Session) ensureHead(n int) error {
	s.mu.Lock()
	active := s.run != nil
	from, to := s.headFrom, s.headTo
	s.mu.Unlock()

	if shouldRestart(n, from, to, active) {
		return s.start(n)
	}
	return nil
}

// How long a restart lets the current pass serve the waiters it is about to
// reach before killing it. Bounded: a slow pass must not hold a seek hostage.
const strandedGrace = 12 * time.Second

// awaitStranded holds a restart while another request waits on a segment the
// running pass reaches soon and the new pass, starting at from, would not.
// Caller holds startMu, so no other restart can slip in while this waits.
func (s *Session) awaitStranded(from int) {
	deadline := time.Now().Add(strandedGrace)
	for time.Now().Before(deadline) {
		s.advanceHead()
		s.mu.Lock()
		run, cur, to := s.run, s.headFrom, s.headTo
		stranded := false
		for n := range s.waiters {
			servedSoon := !shouldRestart(n, cur, to, run != nil)
			byNewPass := !shouldRestart(n, from, from-1, true)
			if servedSoon && !byNewPass {
				stranded = true
			}
		}
		s.mu.Unlock()
		if run == nil || !stranded {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (s *Session) addWaiter(n int) {
	s.mu.Lock()
	if s.waiters == nil {
		s.waiters = map[int]int{}
	}
	s.waiters[n]++
	s.mu.Unlock()
}

func (s *Session) dropWaiter(n int) {
	s.mu.Lock()
	if s.waiters[n]--; s.waiters[n] <= 0 {
		delete(s.waiters, n)
	}
	s.mu.Unlock()
}

// shouldRestart is Jellyfin's rule as a pure decision: relaunch the encoder for
// a segment before the current pass (a backward seek — it only moves forward)
// or far past the segment it is working on now, headTo+1 (a real forward jump,
// not the buffer drifting a few ahead). Everything else the running pass will
// reach, so it is only waited for. from is the pass start, to the highest
// finished segment (from-1 before any).
func shouldRestart(n, from, to int, active bool) bool {
	switch {
	case !active:
		return true
	case n < from:
		return true
	case n > to+1+restartAfterSegments:
		return true
	default:
		return false
	}
}

func (s *Session) start(from int) error {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	return s.startLocked(from)
}

// startLocked is start's body; the caller holds startMu.
func (s *Session) startLocked(from int) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errSessionClosed
	}
	// A caller that raced this one already started a pass reaching this segment;
	// launching a second encoder into the same directory is what corrupts it.
	if r := s.run; r != nil && from >= s.headFrom && from-s.headFrom <= restartAfterSegments {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	// hls.js asks for the resume fragment and segment 0 (its timestamp anchor)
	// together. Killing the pass one request is about to be served by strands
	// that waiter on a segment nobody produces, so requests the current pass
	// reaches soon — and the new pass will not — get a moment to be served.
	s.awaitStranded(from)

	// The pass just killed was most likely half way through a segment, and a
	// file that exists but is short is served as though it were whole.
	if head, killed := s.stop(); killed {
		s.dropTail(head)
	}

	// An earlier pass may have left a stretch of segments starting at this very
	// index. They would read as this pass's progress and send a later request
	// waiting on an encoder nowhere near it, so the contiguous stretch is
	// cleared; this pass rewrites it in order anyway. Files further on, past a
	// gap, stay for an instant seek — they never look like this pass's own.
	for n := from; fileReady(s.SegmentPath(n)); n++ {
		os.Remove(s.SegmentPath(n))
	}

	offset := float64(from) * SegmentSeconds

	// A seek: tell whoever supplies the bytes to fetch there now, rather than
	// waiting for the encoder to open the source and ask for itself.
	if from > 0 && s.onSeek != nil && s.Info.Duration > 0 {
		go s.onSeek(s.ID, offset/s.Info.Duration)
	}

	r, err := s.launch(from)
	if err != nil {
		return err
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		r.cmd.Process.Kill()
		return errSessionClosed
	}
	s.run, s.headFrom, s.headTo, s.touched = r, from, from-1, time.Now()
	s.mu.Unlock()

	go s.await(r)
	return nil
}

func (s *Session) launch(from int) (*run, error) {
	r := &run{from: from, exited: make(chan struct{})}
	r.cmd = exec.Command(s.ffmpeg, s.args(float64(from)*SegmentSeconds, from)...)
	r.cmd.Dir = s.dir
	// Without this an encoder that dies on startup leaves no trace at all, and
	// the only symptom is segments that never appear.
	r.cmd.Stderr = &r.stderr

	if err := r.cmd.Start(); err != nil {
		return nil, fmt.Errorf("start ffmpeg: %w", err)
	}
	s.log.Debug("encoder started", "session", s.ID, "encoder", s.Plan.VideoCodec, "fromSegment", from)
	return r, nil
}

// await sees one encoder process out. Clearing s.run lets a waiter tell "still
// encoding" from "finished". A hardware encoder that dies before writing
// anything is replaced by software here, under the lock, so no waiter sees the gap.
func (s *Session) await(r *run) {
	err := r.cmd.Wait()
	close(r.exited)
	detail := lastLines(r.stderr.String(), encoderLogLines)

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.run != r {
		// Stopped or replaced: whatever it printed on the way out was asked for.
		return
	}
	s.run = nil

	if err == nil {
		if detail != "" {
			s.log.Warn("encoder output", "session", s.ID, "detail", detail)
		}
		return
	}
	s.log.Warn("encoder failed", "session", s.ID, "encoder", s.Plan.VideoCodec,
		"fromSegment", r.from, "err", err, "detail", detail)

	retry := s.canFallBack(r.from, detail)
	// A crash mid-segment leaves a short file that would be served as whole.
	s.dropTail(r.from)
	if !retry || s.closed {
		return
	}

	previous := s.Plan.VideoCodec
	s.Plan.VideoCodec, s.encoder, s.fellBack = softwareEncoder, softwareEncoder, true
	s.log.Warn("falling back to software encoding", "session", s.ID, "was", previous)
	if s.onFallback != nil {
		go s.onFallback(previous)
	}

	next, lerr := s.launch(r.from)
	if lerr != nil {
		s.log.Warn("software encoder did not start", "session", s.ID, "err", lerr)
		return
	}
	s.run, s.headFrom, s.headTo = next, r.from, r.from-1
	go s.await(next)
}

// canFallBack reports a hardware encoder that died at init before writing
// anything. A source read error exits non-zero the same way but software
// cannot help, so it must not trigger a fallback.
func (s *Session) canFallBack(from int, detail string) bool {
	return !s.fellBack && !s.Plan.VideoCopy && usesHardware(s.Plan.VideoCodec) &&
		!fileReady(s.SegmentPath(from)) && encoderInitFailed(detail)
}

// encoderInitFailed spots the stderr of an encoder that could not start on this
// machine, as opposed to one that ran but lost its input.
func encoderInitFailed(detail string) bool {
	d := strings.ToLower(detail)
	for _, m := range []string{
		"initializeencoder", "openencodesessionex", "cannot load", "cannot init",
		"no capable devices", "doesn't support", "no device", "error initializing",
		"init failed", "not supported", "failed to initialise", "failed to initialize",
	} {
		if strings.Contains(d, m) {
			return true
		}
	}
	return false
}

// dropTail removes the last segment the killed pass wrote. Only the top of the
// contiguous run from `from` belongs to that pass; a higher number past a gap
// is from an earlier, complete pass.
func (s *Session) dropTail(from int) {
	last := from - 1
	for n := from; fileReady(s.SegmentPath(n)); n++ {
		last = n
	}
	if last >= from {
		os.Remove(s.SegmentPath(last))
	}
}

func lastLines(s string, n int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	for i, l := range lines {
		lines[i] = strings.TrimSpace(l)
	}
	return strings.Join(lines, " | ")
}

// stop kills the running pass, if any, and reports which segment it had
// started from. It returns only once the process has gone; until then its
// segment cannot be removed.
func (s *Session) stop() (head int, killed bool) {
	s.mu.Lock()
	r := s.run
	s.run = nil
	s.mu.Unlock()

	if r == nil {
		return 0, false
	}
	r.cmd.Process.Kill()
	select {
	case <-r.exited:
	case <-time.After(3 * time.Second):
	}
	return r.from, true
}
