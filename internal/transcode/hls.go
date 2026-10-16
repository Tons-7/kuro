package transcode

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"kuro/internal/sound"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Six seconds: short enough to seek responsively, long enough that a film's playlist stays manageable.
const (
	SegmentSeconds = 6.0

	// A forward seek within this many segments waits for the encoder instead of restarting it.
	restartAfterSegments = 4

	sessionIdle = 10 * time.Minute

	// What always works, and what a hardware encoder that fails at its first frame is swapped for.
	softwareEncoder = "libx264"

	// The cause sits just above ffmpeg's closing thread-exit lines.
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
	// Segments requests wait on now, so a restart lets their pass finish first (as Jellyfin does).
	waiters  map[int]int
	headFrom int
	// headTo: the current pass's highest finished segment (headFrom-1 before any), where it really is.
	headTo  int
	touched time.Time
	// The encoded track's index in Info.Audio and the preference that picked
	// it. Only a changed preference re-picks: re-picking drops every segment.
	audioTrack int
	audioPref  string
	// What is done to the audio on the way out; see voice.go.
	sound sound.Options
	// Set once the session is released, so a late waiter's restart branch does
	// not relaunch ffmpeg into a directory that is being torn down.
	closed bool
	// Serialises a start (kill, launch, publish), so two encoders never share a directory.
	startMu sync.Mutex
	// Holders of this session; closing once mustn't stop what another watches.
	refs int
	// A software failure is reported, not retried.
	fellBack bool
	// A process beside each pass writes subtitle lines as they are read.
	liveSubs bool
	// The last pass to end on its own: its subtitle process may still be finishing.
	finished *run
	// ended: the last pass to finish, its cut tail dropped if it stopped short, so every file it left is whole.
	ended *run
}

// run is one encoder process: what it was started from and when it has gone,
// so the file it was writing can be cleaned up only once it has let go of it.
type run struct {
	cmd    *exec.Cmd
	stderr strings.Builder
	from   int
	exited chan struct{}
	// Older files past a gap are an earlier pass's: good for a seek, no proof of progress.
	began time.Time
	// The process writing subtitle lines beside this pass, if any: whether it was told to stop, and is running.
	subs        *exec.Cmd
	subsStopped atomic.Bool
	subsAlive   atomic.Bool
}

// wrote reports whether segment n is this pass's own output.
func (r *run) wrote(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Size() > 0 && !info.ModTime().Before(r.began)
}

// SeekHook is told where playback jumped (a fraction), so the source fetches there.
type SeekHook func(sessionID string, fraction float64)

type Manager struct {
	ffmpeg  string
	prober  *Prober
	root    string
	encoder string
	onSeek  SeekHook
	onIdle  func(id string)
	// Bytes of a session's source that have arrived; nil when nothing can say.
	arriving func(ctx context.Context, id string) int64
	log      *slog.Logger

	mu       sync.Mutex
	sessions map[string]*Session
}

func (m *Manager) OnSeek(hook SeekHook) { m.onSeek = hook }

// Installed checks ffmpeg and ffprobe on each call; Setup can add them live.
func (m *Manager) Installed() bool {
	for _, p := range []string{m.ffmpeg, m.prober.ffprobe} {
		if _, err := os.Stat(p); err != nil {
			return false
		}
	}
	return true
}

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

// Purge clears a previous run's output, as no session survives a restart.
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

// How many more times a probe waits out its limit while the file's start is still arriving.
const probeExtensions = 4

// OnArriving sets how many bytes of a session's source have arrived, for a probe to tell slow from stopped.
func (m *Manager) OnArriving(bytes func(ctx context.Context, id string) int64) { m.arriving = bytes }

// probeWhileArriving reads the start of the file for as long as its download keeps moving: a slow
// connection buffers, as in any player, and only a download that has stopped is given up on.
func (m *Manager) probeWhileArriving(ctx context.Context, id, source string) (*MediaInfo, error) {
	var seen int64
	if m.arriving != nil {
		seen = m.arriving(ctx, id)
	}
	for extended := 0; ; extended++ {
		info, err := m.prober.Probe(ctx, source)
		if err == nil || !errors.Is(err, ErrProbeTimeout) || m.arriving == nil || extended >= probeExtensions {
			return info, err
		}
		got := m.arriving(ctx, id)
		if got <= seen {
			return nil, err
		}
		m.log.Info("the start of the file is still arriving, waiting on", "session", id, "arrivedMB", got>>20)
		seen = got
	}
}

func (m *Manager) Open(ctx context.Context, id, source string) (*Session, error) {
	return m.OpenProbing(ctx, id, source, source)
}

// OpenProbing probes a torrent from disk but encodes through the engine, whose reads move the window.
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

	var info *MediaInfo
	var err error
	probed := "source"
	if probeSource == source {
		info, err = m.probeWhileArriving(ctx, id, source)
	} else if info, err = m.prober.Probe(ctx, probeSource); err == nil {
		probed = "disk"
	} else {
		m.log.Warn("probe from disk failed, reading through the engine",
			"session", id, "err", err)
		info, err = m.probeWhileArriving(ctx, id, source)
		probed = "engine"
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
		liveSubs:   liveSubtitlesWanted(source),
	}

	m.mu.Lock()
	// Another open won the race to this episode; keep theirs, both counted on one encoder.
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
		"duration", int(info.Duration), "plan", s.Plan.Reason, "probed", probed,
		"audio", len(info.Audio), "subtitles", len(info.Subtitles))
	return s, nil
}

func (m *Manager) currentEncoder() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.encoder
}

// SetEncoder sets new sessions' encoder, for ffmpeg arriving after startup detection.
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

// Close releases one hold and reports the last; stopping the download belongs behind a true.
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

	if !last {
		return false
	}
	return m.teardown(id, s)
}

// teardown stops an unlinked session; true, and its shared directory gone, when no replacement opened.
func (m *Manager) teardown(id string, s *Session) bool {
	s.stop()
	m.mu.Lock()
	owner := m.sessions[id]
	m.mu.Unlock()
	if owner != nil && owner != s {
		return false
	}
	os.RemoveAll(s.dir)
	return true
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

// discardSession tears down an unlinked session, its shared directory only if unowned.
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

// Sources lists what open sessions read, since a pack's torrent may serve another episode still.
func (m *Manager) Sources() []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]string, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, s.Source)
	}
	return out
}

// liveSubtitlesWanted: only a torrent still arriving gets lines written beside the picture.
func liveSubtitlesWanted(source string) bool { return strings.Contains(source, "/torrents/") }

// Reap discards sessions nobody is watching. Each one holds an ffmpeg process and a directory of segments.
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

// segmentCount is the playlist's segments; a sub-second remainder folds into the last, as the muxer does.
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

// Prestart launches the encoder at a segment not on disk if nothing runs, and
// returns at once. It never moves a running pass; only the player's requests do.
func (s *Session) Prestart(from int) error {
	if fileReady(s.SegmentPath(from)) {
		return nil
	}
	return s.startIfIdle(from)
}

// startIfIdle launches a pass at from only when none exists, deciding under the
// lock that publishes one; a separate running() check races a start mid-launch.
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

// WaitInit waits for the init segment, starting a pass at from if none runs; never redirecting one.
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

// WaitSegment returns once a segment exists, starting or moving the encoder if needed.
func (s *Session) WaitSegment(ctx context.Context, n int, timeout time.Duration) (string, error) {
	path := s.SegmentPath(n)
	if segmentReady(path) {
		s.touch()
		return path, nil
	}

	// Known to restarts, so a pass about to serve this request is not killed under it.
	s.addWaiter(n)
	defer s.dropWaiter(n)

	if err := s.ensureHead(n); err != nil {
		return "", err
	}

	deadline := time.After(timeout)
	var restarted bool
	for {
		s.advanceHead()

		// Complete once the next appears, it is the last advertised, or a pass that read to the end wrote it.
		ready := segmentReady(path)
		if ready && (s.nextStarted(n) || n >= s.Segments-1 || s.wroteWhole(path)) {
			s.touch()
			return path, nil
		}

		// The pass ended short (copy cuts on keyframes, or a read failed); one pass aimed at the segment writes it.
		if !s.running() {
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

// produced lists what the encoder wrote, telling a dead one from one writing elsewhere.
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

// cutShort: a stream stopped before its end; a file on disk can't arrive later, so its pass stands as it ended.
func cutShort(source, detail string) bool { return remote(source) && endedEarly(detail) }

// endedEarly: ffmpeg's word that its input stopped before its end (a cut HTTP body, a failed read).
func endedEarly(detail string) bool {
	for _, sign := range []string{"ends prematurely", "Error during demuxing", "I/O error", "Input/output error"} {
		if strings.Contains(detail, sign) {
			return true
		}
	}
	return false
}

// segmentReady: a segment holding media, not the bare styp a pass cut off at a segment's start leaves.
func segmentReady(path string) bool {
	f, err := openShared(path)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	return bytes.Contains(head[:n], []byte("moof"))
}

// wroteWhole: path came from the last finished pass, which left only whole files.
func (s *Session) wroteWhole(path string) bool {
	s.mu.Lock()
	r := s.ended
	s.mu.Unlock()
	return r != nil && r.wrote(path)
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

// advanceHead moves headTo up to the current pass's highest finished segment.
func (s *Session) advanceHead() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.run == nil {
		return
	}
	// Done once the next has started (WaitSegment's rule), and only this pass's files count.
	for s.run.wrote(s.SegmentPath(s.headTo+1)) && s.run.wrote(s.SegmentPath(s.headTo+2)) {
		s.headTo++
	}
}

// nextStarted proves n complete: the next file, from the running pass if it writes n.
func (s *Session) nextStarted(n int) bool {
	s.mu.Lock()
	r := s.run
	s.mu.Unlock()
	next := s.SegmentPath(n + 1)
	if r != nil && n >= r.from {
		return r.wrote(next)
	}
	return fileReady(next)
}

// ensureHead starts or moves the encoder when segment n is out of the current
// pass's reach (see shouldRestart). Finished segments never get here.
func (s *Session) ensureHead(n int) error {
	// A stale head reads a pass waiting on data as left behind, and restarting it drops a whole segment.
	s.advanceHead()
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

// awaitStranded holds a restart while a waiter's segment is near for this pass but not the new one.
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

// shouldRestart is Jellyfin's rule: relaunch for a segment before the pass or far past where it is.
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

	// hls.js asks for the resume point and segment 0 at once; a soon-served waiter gets a moment first.
	s.awaitStranded(from)

	// The pass just killed was most likely half way through a segment, and a
	// file that exists but is short is served as though it were whole.
	if killed := s.stop(); killed != nil {
		s.dropTail(killed)
	}

	// Segments an earlier pass left from here on would read as this pass's
	// progress; clear the contiguous run. Ones past a gap stay for fast seeks.
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
		r.stopSubtitles()
		return errSessionClosed
	}
	s.run, s.headFrom, s.headTo, s.touched = r, from, from-1, time.Now()
	s.mu.Unlock()

	go s.await(r)
	return nil
}

func (s *Session) launch(from int) (*run, error) {
	r := &run{from: from, exited: make(chan struct{}), began: time.Now()}
	r.cmd = exec.Command(s.ffmpeg, s.args(float64(from)*SegmentSeconds, from)...)
	r.cmd.Dir = s.dir
	// Or an encoder dying on startup leaves no trace beyond segments that never come.
	r.cmd.Stderr = &r.stderr

	if err := r.cmd.Start(); err != nil {
		return nil, fmt.Errorf("start ffmpeg: %w", err)
	}
	s.log.Debug("encoder started", "session", s.ID, "encoder", s.Plan.VideoCodec, "fromSegment", from)
	s.startSubtitles(r)
	return r, nil
}

// await sees an encoder out, swapping a hardware one that died at once for software under the lock.
func (s *Session) await(r *run) {
	err := r.cmd.Wait()
	close(r.exited)
	detail := lastLines(r.stderr.String(), encoderLogLines)

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.run != r {
		// Stopped or replaced: whatever it printed on the way out was asked for.
		r.stopSubtitles()
		return
	}
	s.run = nil

	if err == nil {
		// Its subtitle process may be a few lines behind; stop ends it if the session goes first.
		s.finished = r
		if detail != "" {
			s.log.Warn("encoder output", "session", s.ID, "detail", detail)
		}
		// ffmpeg exits cleanly on a stream cut short too, its last segment short; the next ask rewrites it.
		if cutShort(s.Source, detail) {
			s.log.Warn("encoder ended before the source did", "session", s.ID, "fromSegment", r.from)
			s.dropTail(r)
		}
		s.ended = r
		return
	}
	r.stopSubtitles()
	s.log.Warn("encoder failed", "session", s.ID, "encoder", s.Plan.VideoCodec,
		"fromSegment", r.from, "err", err, "detail", detail)

	retry := s.canFallBack(r.from, detail)
	// A crash mid-segment leaves a short file that would be served as whole.
	s.dropTail(r)
	if !retry || s.closed {
		// Its cut tail gone, what it wrote is whole; a retry instead rewrites from where it began.
		s.ended = r
		return
	}

	previous := s.switchToSoftware()
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

// switchToSoftware moves to libx264, dropping the hardware encoder's segments and init, which don't match.
func (s *Session) switchToSoftware() (previous string) {
	previous = s.Plan.VideoCodec
	s.Plan.VideoCodec, s.encoder, s.fellBack = softwareEncoder, softwareEncoder, true
	if fileReady(s.InitPath()) {
		s.wipeOutput()
	}
	return previous
}

// wipeOutput removes every segment and the init file.
func (s *Session) wipeOutput() {
	for n := 0; n < s.Segments; n++ {
		os.Remove(s.SegmentPath(n))
	}
	os.Remove(s.InitPath())
}

// canFallBack: a hardware encoder died at init; a source read error looks alike but software can't help.
func (s *Session) canFallBack(from int, detail string) bool {
	return !s.fellBack && !s.Plan.VideoCopy && usesHardware(s.Plan.VideoCodec) &&
		!fileReady(s.SegmentPath(from)) && encoderInitFailed(detail)
}

// encoderInitFailed spots an encoder that couldn't start here, not one that lost its input.
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

// dropTail removes the last segment r wrote, likely cut short; an older pass's files right after it stay.
func (s *Session) dropTail(r *run) {
	last := -1
	for n := r.from; r.wrote(s.SegmentPath(n)); n++ {
		last = n
	}
	if last >= 0 {
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

// stop kills the running pass and returns it, nil when none ran, once the process has gone.
func (s *Session) stop() *run {
	s.mu.Lock()
	r := s.run
	s.run, s.ended = nil, nil
	if done := s.finished; done != nil {
		done.stopSubtitles()
		s.finished = nil
	}
	s.mu.Unlock()

	if r == nil {
		return nil
	}
	r.cmd.Process.Kill()
	r.stopSubtitles()
	select {
	case <-r.exited:
	case <-time.After(3 * time.Second):
	}
	return r
}
