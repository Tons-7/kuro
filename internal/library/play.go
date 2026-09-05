package library

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"kuro/internal/player"
	"kuro/internal/score"
	"kuro/internal/store"
	"kuro/internal/torrent"
	"kuro/internal/transcode"
)

// Player is the local playback backend. Only mpv implements it today; the
// browser path needs no process, just the stream URL.
type Player interface {
	Play(ctx context.Context, opts player.Options) error
	Events() <-chan player.Event
	Running() bool
	Stop()
}

type Playback struct {
	store    *store.Store
	finder   *Finder
	torrent  *torrent.Client
	player   Player
	sync     *Sync
	enricher *Enricher
	prefetch *Prefetcher
	cache    *Cache
	prober   Prober
	// Named before a search: cours are told apart by their siblings' titles.
	relations *Relations
	cacheDir  string
	log       *slog.Logger

	// mpv has one event channel, so a tracker left over from the previous
	// episode would consume this one's events under the old number.
	trackGen atomic.Uint64
}

// Prober reads an episode's length and chapters; optional.
type Prober interface {
	Probe(ctx context.Context, source string) (*transcode.MediaInfo, error)
}

// Enough for a header read, not enough to hold up mpv's launch.
const probeDeadline = 12 * time.Second

// A relation walk is a few AniList round trips; the search after it takes longer.
const relationsDeadline = 10 * time.Second

func NewPlayback(s *store.Store, f *Finder, tc *torrent.Client, p Player, cacheDir string, log *slog.Logger) *Playback {
	return &Playback{store: s, finder: f, torrent: tc, player: p, cacheDir: cacheDir, log: log}
}

// All optional: playback works without progress write-back, skip timestamps
// or prefetching.
func (p *Playback) WithSync(s *Sync) *Playback             { p.sync = s; return p }
func (p *Playback) WithEnricher(e *Enricher) *Playback     { p.enricher = e; return p }
func (p *Playback) WithPrefetcher(f *Prefetcher) *Playback { p.prefetch = f; return p }
func (p *Playback) WithCache(c *Cache) *Playback           { p.cache = c; return p }
func (p *Playback) WithProber(pr Prober) *Playback         { p.prober = pr; return p }
func (p *Playback) WithRelations(r *Relations) *Playback   { p.relations = r; return p }

type PlayRequest struct {
	AnimeID  int
	Episode  int
	Season   int
	InfoHash string
	External bool
	AllowRaw bool
	Prefs    score.Preferences
}

type Session struct {
	AnimeID   int     `json:"animeId"`
	Episode   int     `json:"episode"`
	Title     string  `json:"title"`
	Source    string  `json:"source"`
	InfoHash  string  `json:"infoHash,omitempty"`
	TorrentID int     `json:"torrentId,omitempty"`
	FileIndex int     `json:"fileIndex"`
	StreamURL string  `json:"streamUrl"`
	LocalPath string  `json:"localPath,omitempty"`
	StartAt   float64 `json:"startAt"`
	Player    string  `json:"player"`
}

// Start resolves a release, hands it to the torrent engine, waits until it can
// actually be read, and begins playback from wherever the user left off.
func (p *Playback) Start(ctx context.Context, req PlayRequest) (*Session, error) {
	// A local file beats any torrent. Skipped when a release was picked by hand.
	if req.InfoHash == "" {
		if session, ok, err := p.startLocal(ctx, req); err != nil {
			return nil, err
		} else if ok {
			return session, nil
		}
	}

	// The series page pre-resolves this episode, so play can arrive mid-resolve.
	// Waiting beats repeating the search and avoids picking a different release.
	if req.InfoHash == "" {
		p.prefetch.AwaitPrepare(ctx, req.AnimeID, req.Episode)
	}

	// Already downloading or downloaded: the release is known, so searching
	// every indexer again answers a question already on disk.
	if req.InfoHash == "" {
		if session, ok := p.reattach(ctx, req); ok {
			return session, nil
		}
	}

	release, added, live, file, fileIndex, err := p.attach(ctx, req)
	if err != nil {
		return nil, err
	}

	if err := p.store.RecordTorrent(ctx, store.TorrentRecord{
		InfoHash:  release.Torrent.InfoHash,
		RqbitID:   live.ID,
		Name:      added.Details.Name,
		TotalSize: file.Length,
		AnimeID:   req.AnimeID,
		EpKey:     epKey(req.Episode),
		FileIndex: fileIndex,
		FilePath:  file.Name,
		Manual:    req.InfoHash != "",
	}); err != nil {
		p.log.Warn("record torrent", "err", err)
	}
	dropSuperseded(ctx, p.store, p.torrent, p.log, req.AnimeID, epKey(req.Episode))

	resume, _ := p.store.ResumeAt(ctx, req.AnimeID, epKey(req.Episode))
	session := &Session{
		AnimeID:   req.AnimeID,
		Episode:   req.Episode,
		Title:     release.Torrent.Title,
		InfoHash:  release.Torrent.InfoHash,
		TorrentID: live.ID,
		FileIndex: fileIndex,
		StreamURL: p.torrent.StreamURL(live.ID, fileIndex),
		Source:    "torrent",
		StartAt:   resume,
		Player:    "browser",
	}

	if req.External {
		if err := p.launch(ctx, req, session); err != nil {
			return nil, err
		}
		session.Player = "mpv"
	}

	p.prefetchAfter(req)
	return session, nil
}

// prefetchAfter readies what the viewer likely plays next; run from every start
// path. Prepare resolves the next release (cheap, default); Next downloads it (opt-in).
func (p *Playback) prefetchAfter(req PlayRequest) {
	p.prefetch.Prepare(req.AnimeID, req.Episode, req.Season, req.Prefs)
	p.prefetch.Next(req.AnimeID, req.Episode, req.Season, req.Prefs)
}

// startLocal plays a scanned file. It reports false rather than an error when
// there is nothing on disk, so the caller falls through to torrents.
func (p *Playback) startLocal(ctx context.Context, req PlayRequest) (*Session, bool, error) {
	f, err := p.store.LocalEpisode(ctx, req.AnimeID, req.Episode)
	if err != nil || f.ID == 0 {
		return nil, false, nil
	}
	if _, err := os.Stat(f.Path); err != nil {
		// Recorded but gone: the next scan will mark it missing.
		p.log.Warn("local file unreadable, falling back to torrent", "path", f.Path, "err", err)
		return nil, false, nil
	}

	resume, _ := p.store.ResumeAt(ctx, req.AnimeID, epKey(req.Episode))
	session := &Session{
		AnimeID:   req.AnimeID,
		Episode:   req.Episode,
		Title:     filepath.Base(f.Path),
		Source:    "local",
		StreamURL: fmt.Sprintf("/api/local/%d/stream", f.ID),
		LocalPath: f.Path,
		StartAt:   resume,
		Player:    "browser",
	}

	if req.External {
		if err := p.launch(ctx, req, session); err != nil {
			return nil, false, err
		}
		session.Player = "mpv"
	}

	p.log.Info("playing local file", "anime", req.AnimeID, "episode", req.Episode, "path", f.Path)
	p.prefetchAfter(req)
	return session, true, nil
}

// reattach resumes the release already held. Failures report false rather than
// an error, so the caller falls through to the full search.
func (p *Playback) reattach(ctx context.Context, req PlayRequest) (*Session, bool) {
	rec, ok, err := p.store.TorrentForEpisode(ctx, req.AnimeID, epKey(req.Episode))
	if err != nil || !ok {
		return nil, false
	}

	// Recorded before identity was enforced, this can be another show entirely.
	// Searching again replaces it, which also clears it from the engine.
	if !rec.Manual && !heldNamesShow(ctx, p.store, req.AnimeID, rec.Name) {
		p.log.Warn("held release names another show, searching again",
			"anime", req.AnimeID, "episode", req.Episode, "release", rec.Name)
		return nil, false
	}

	// Ids are per engine session, so the recorded one may now name a different
	// torrent; the hash is what identifies it.
	live, err := p.torrent.Live(ctx)
	if err != nil {
		return nil, false
	}
	id, held := live[strings.ToLower(rec.InfoHash)]
	if !held {
		return nil, false
	}

	if err := p.torrent.Start(ctx, id); err != nil {
		p.log.Debug("resume download", "torrent", id, "err", err)
	}
	if err := p.warmed(ctx, id, rec.FileIndex); err != nil {
		p.log.Warn("held release is not delivering, searching again",
			"anime", req.AnimeID, "episode", req.Episode, "err", err)
		return nil, false
	}

	if err := p.store.RecordTorrent(ctx, store.TorrentRecord{
		InfoHash: rec.InfoHash, RqbitID: id, Name: rec.Name, TotalSize: rec.TotalSize,
		AnimeID: req.AnimeID, EpKey: epKey(req.Episode),
		FileIndex: rec.FileIndex, FilePath: rec.FilePath, Manual: rec.Manual,
	}); err != nil {
		p.log.Warn("record torrent", "err", err)
	}

	resume, _ := p.store.ResumeAt(ctx, req.AnimeID, epKey(req.Episode))
	session := &Session{
		AnimeID:   req.AnimeID,
		Episode:   req.Episode,
		Title:     rec.Name,
		InfoHash:  rec.InfoHash,
		TorrentID: id,
		FileIndex: rec.FileIndex,
		StreamURL: p.torrent.StreamURL(id, rec.FileIndex),
		Source:    "torrent",
		StartAt:   resume,
		Player:    "browser",
	}

	if req.External {
		if err := p.launch(ctx, req, session); err != nil {
			return nil, false
		}
		session.Player = "mpv"
	}

	p.log.Info("resumed the release already held", "anime", req.AnimeID, "episode", req.Episode)
	p.prefetchAfter(req)
	return session, true
}

// A tracker's seeder count is routinely wrong, so delivery has to be measured.
// A timeout alone cannot tell dead from slow; arriving bytes can.
const (
	firstBytesDeadline = 20 * time.Second
	warmingDeadline    = 70 * time.Second
	// Long enough for a live swarm to produce a peer, short enough that four
	// dead candidates cost half a minute rather than six.
	peerProbeDeadline = 8 * time.Second
	// Re-checking a part file is disk work, not swarm work, and rqbit blocks reads
	// until it finishes; its own allowance so a held release isn't judged dead.
	engineReadyDeadline = 4 * time.Minute
)

// A head start for the better release before a lower-ranked one joins the race,
// and how many warm at once. Vars so a test can tighten the timing.
var (
	raceStagger = 6 * time.Second
	raceWidth   = 3
)

func (p *Playback) warmed(ctx context.Context, id, fileIndex int) error {
	// Waited for separately so the deadlines below measure only delivery. Returns
	// at once when live, but extends while a check is still reading the file.
	ready, cancelReady := context.WithTimeout(ctx, engineReadyDeadline)
	readyErr := p.torrent.WaitLive(ready, id, engineReadyDeadline)
	cancelReady()
	if readyErr != nil {
		return readyErr
	}

	// Only the head — waiting on the end-of-file seek index too left ready episodes
	// spinning. A short first look, since a dead release looks slow until peers are
	// counted.
	warm, cancel := context.WithTimeout(ctx, peerProbeDeadline)
	err := p.torrent.PrewarmHead(warm, id, fileIndex, 2<<20)
	cancel()
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return err
	}

	stats, statErr := p.torrent.Stats(ctx, id)
	if statErr != nil {
		return err
	}

	// Nothing downloaded and no peers: the tracker's seeder count was wrong, and
	// the time belongs to the next candidate.
	if stats.ProgressBytes == 0 && stats.Peers() == 0 {
		return fmt.Errorf("no peers after %s", peerProbeDeadline)
	}

	p.log.Info("release is slow, waiting",
		"torrent", id, "bytes", stats.ProgressBytes, "peers", stats.Peers())

	// Connected, or already delivering: worth the full allowance.
	warm, cancel = context.WithTimeout(ctx, firstBytesDeadline+warmingDeadline)
	defer cancel()
	return p.torrent.PrewarmHead(warm, id, fileIndex, 2<<20)
}

// Every candidate costs a metadata lookup from the swarm before it can be
// judged. Looking ahead makes the wait the slowest of them, not the sum.
const inspectAhead = 3

type inspection struct {
	torrent *torrent.Torrent
	err     error
}

func (p *Playback) inspectAhead(ctx context.Context, candidates []score.Result) []chan inspection {
	pending := make([]chan inspection, len(candidates))
	for i := 0; i < len(candidates) && i < inspectAhead; i++ {
		pending[i] = make(chan inspection, 1)
		go func(i int, magnet string) {
			got, err := p.torrent.Inspect(ctx, magnet)
			pending[i] <- inspection{torrent: got, err: err}
		}(i, candidates[i].Torrent.Magnet())
	}
	return pending
}

// awaitInspect takes the lookup already in flight, or runs one for a candidate
// too far down the list to have been prefetched.
func (p *Playback) awaitInspect(
	ctx context.Context, pending []chan inspection, i int, magnet string,
) (*torrent.Torrent, error) {
	if i >= len(pending) || pending[i] == nil {
		return p.torrent.Inspect(ctx, magnet)
	}
	select {
	case got := <-pending[i]:
		return got.torrent, got.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// attach delivers the best release that actually seeds. Candidates race a few at
// a time, but a lower-ranked one wins only after every better one has failed.
func (p *Playback) attach(ctx context.Context, req PlayRequest) (
	score.Result, *torrent.Torrent, *torrent.Torrent, torrent.File, int, error,
) {
	// A release resolved while the series page was open skips the indexer search,
	// the slow part; a full search still follows if it is gone or does not seed.
	if p.prefetch != nil {
		if rel, ok := p.prefetch.TakePrepared(req.AnimeID, req.Episode); ok {
			if r, a, l, f, fi, err := p.attachFrom(ctx, req, []score.Result{rel}); err == nil {
				return r, a, l, f, fi, nil
			}
			p.log.Info("resolved release did not deliver, searching",
				"anime", req.AnimeID, "episode", req.Episode)
		}
	}

	candidates, err := p.candidates(ctx, req)
	if err != nil {
		return score.Result{}, nil, nil, torrent.File{}, 0, err
	}
	return p.attachFrom(ctx, req, candidates)
}

func (p *Playback) attachFrom(ctx context.Context, req PlayRequest, candidates []score.Result) (
	score.Result, *torrent.Torrent, *torrent.Torrent, torrent.File, int, error,
) {
	// Only what this call starts may be discarded.
	held, err := p.torrent.Live(ctx)
	if err != nil {
		p.log.Warn("list live torrents", "err", err)
		held = map[string]int{}
	}

	pending := p.inspectAhead(ctx, candidates)
	return p.race(ctx, req, candidates, pending, held)
}

// attempt is one candidate's outcome. On failure it has already discarded any
// torrent it started, unless that torrent was held before this call.
type attempt struct {
	index       int
	release     score.Result
	added, live *torrent.Torrent
	file        torrent.File
	fileIndex   int
	alreadyHeld bool
	err         error
}

// race warms candidates in parallel, capped and staggered, returning the
// lowest-index one that delivers once every better-ranked one has resolved.
func (p *Playback) race(
	ctx context.Context, req PlayRequest, candidates []score.Result,
	pending []chan inspection, held map[string]int,
) (score.Result, *torrent.Torrent, *torrent.Torrent, torrent.File, int, error) {
	var zero score.Result
	stagger := raceStagger
	n := len(candidates)

	raceCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	results := make(chan attempt)
	slots := make(chan struct{}, raceWidth)
	go func() {
		for i := range n {
			select {
			case <-raceCtx.Done():
				return
			case slots <- struct{}{}:
			}
			go func(i int) {
				defer func() { <-slots }()
				a := p.tryCandidate(raceCtx, req, candidates[i], pending, held, i)
				select {
				case results <- a:
				case <-raceCtx.Done():
					// The race is already decided; a stray winner must not leak.
					if a.err == nil && a.live != nil && !a.alreadyHeld {
						p.discard(context.WithoutCancel(ctx), a.live.ID)
					}
				}
			}(i)
			select {
			case <-raceCtx.Done():
				return
			case <-time.After(stagger):
			}
		}
	}()

	outcomes := make([]*attempt, n)
	var lastErr error
	for range n {
		var a attempt
		select {
		case a = <-results:
		case <-ctx.Done():
			return zero, nil, nil, torrent.File{}, 0, ctx.Err()
		}
		outcomes[a.index] = &a
		if a.err != nil {
			lastErr = a.err
		}
		// Commit to the first success whose every better-ranked rival has
		// already resolved; a nil ahead of it means a better one is still racing.
		for i := range n {
			if outcomes[i] == nil {
				break
			}
			if outcomes[i].err == nil {
				cancel()
				go p.discardLosers(ctx, outcomes, i)
				o := outcomes[i]
				return o.release, o.added, o.live, o.file, o.fileIndex, nil
			}
		}
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("no playable release for episode %d", req.Episode)
	}
	return zero, nil, nil, torrent.File{}, 0, fmt.Errorf(
		"tried %d release(s) for episode %d without one that would download: %w",
		n, req.Episode, lastErr)
}

// discardLosers drops the torrents of races that finished but were not chosen.
// Ones still running when the winner is picked discard themselves.
func (p *Playback) discardLosers(ctx context.Context, outcomes []*attempt, winner int) {
	clean := context.WithoutCancel(ctx)
	for _, a := range outcomes {
		if a != nil && a.index != winner && a.err == nil && a.live != nil && !a.alreadyHeld {
			p.discard(clean, a.live.ID)
		}
	}
}

// tryCandidate inspects, adds and warms one release, self-discarding on any
// failure so the caller only cleans up the winners it did not pick.
func (p *Playback) tryCandidate(
	ctx context.Context, req PlayRequest, release score.Result,
	pending []chan inspection, held map[string]int, index int,
) attempt {
	fail := func(err error) attempt { return attempt{index: index, err: err} }

	added, err := p.awaitInspect(ctx, pending, index, release.Torrent.Magnet())
	if err != nil {
		p.log.Warn("release unusable, trying the next", "title", release.Torrent.Title, "err", err)
		return fail(fmt.Errorf("inspect torrent: %w", err))
	}
	_, alreadyHeld := held[strings.ToLower(added.Details.InfoHash)]

	// Only the requested file is downloaded from a season pack, so it has to be
	// identified before adding, by whichever numbers this release may use.
	file, fileIndex, ok := torrent.PickEpisode(added.Details.Files, release.Numbers...)
	if !ok {
		p.log.Warn("episode missing from release", "title", added.Details.Name)
		return fail(fmt.Errorf("episode %d not found inside %q", req.Episode, added.Details.Name))
	}

	live, err := p.torrent.Add(ctx, release.Torrent.Magnet(), file)
	if err != nil {
		return fail(fmt.Errorf("add torrent: %w", err))
	}
	// It may have been paused when the last viewer left.
	if err := p.torrent.Start(ctx, live.ID); err != nil {
		p.log.Debug("resume download", "torrent", live.ID, "err", err)
	}

	// Streaming before the engine reports live fails with an opaque 500.
	if err := p.warmed(ctx, live.ID, fileIndex); err != nil {
		p.log.Warn("release is not seeded, trying the next",
			"title", release.Torrent.Title, "seeders", release.Torrent.Seeders)
		if !alreadyHeld {
			p.discard(context.WithoutCancel(ctx), live.ID)
		}
		return fail(fmt.Errorf("no data from the swarm: %w", err))
	}
	return attempt{
		index: index, release: release, added: added, live: live,
		file: file, fileIndex: fileIndex, alreadyHeld: alreadyHeld,
	}
}

// Suspend stops fetching an episode nobody is watching; without it the engine
// pulls the whole file long after the viewer left. The caller checks nothing
// else (a season pack) still needs the torrent.
func (p *Playback) Suspend(ctx context.Context, animeID, episode int) {
	if p.torrent == nil || animeID == 0 || episode <= 0 {
		return
	}
	// After the unpin below, so the episode just left can go too.
	defer p.autoDelete(animeID)

	hash, index, err := p.store.CachedFile(ctx, animeID, epKey(episode))
	if err != nil || hash == "" {
		return
	}

	// Unpin first, before the early returns below: a browser session otherwise
	// held the pin until restart, and the downloads list kept calling it playing.
	if err := p.store.PinCache(ctx, hash, index, false); err != nil {
		p.log.Warn("unpin", "anime", animeID, "episode", episode, "err", err)
	}

	live, err := p.torrent.Live(ctx)
	if err != nil {
		return
	}
	id, ok := live[strings.ToLower(hash)]
	if !ok {
		return
	}

	if stats, err := p.torrent.Stats(ctx, id); err == nil && stats.Finished {
		return
	}

	// "Keep whole episode": finish what was started so revisiting it needs no
	// swarm. On by default; off, every download looked stalled when the player closed.
	if global, err := p.store.Prefs(ctx, 0); err == nil && global.Bool("cache.prefetch_full") {
		p.log.Info("download left running to finish the episode",
			"anime", animeID, "episode", episode)
		return
	}
	if err := p.torrent.Pause(ctx, id); err != nil {
		p.log.Debug("pause download", "torrent", id, "err", err)
		return
	}
	p.log.Info("download paused, nothing watching it", "anime", animeID, "episode", episode)
}

// rqbit reserves a file's full size up front, so an abandoned candidate costs
// its whole length until it is removed.
func (p *Playback) discard(ctx context.Context, id int) {
	if err := p.torrent.Delete(ctx, id); err != nil {
		p.log.Warn("remove abandoned release", "torrent", id, "err", err)
		return
	}
	p.log.Info("abandoned release removed", "torrent", id)
}

// candidates orders the releases worth attempting, best first.
func (p *Playback) candidates(ctx context.Context, req PlayRequest) ([]score.Result, error) {
	req.Prefs.AllowRaw = req.AllowRaw

	// Bounded: a throttled AniList must not hold up every play.
	if p.relations != nil {
		named, cancel := context.WithTimeout(ctx, relationsDeadline)
		if err := p.relations.Ensure(named, req.AnimeID); err != nil {
			p.log.Warn("franchise for matching", "anime", req.AnimeID, "err", err)
		}
		cancel()
	}

	found, err := p.finder.Find(ctx, Request{
		AnimeID: req.AnimeID,
		Episode: req.Episode,
		Season:  req.Season,
		Prefs:   req.Prefs,
	})
	if err != nil {
		return nil, err
	}

	// An explicit choice from the manual picker is the only one to try.
	if req.InfoHash != "" {
		for _, r := range found.Results {
			if r.Torrent.InfoHash == req.InfoHash {
				return []score.Result{r}, nil
			}
		}
		return nil, fmt.Errorf("release %s not found", req.InfoHash)
	}

	if found.Best == nil {
		return nil, noRelease(found, req)
	}

	// The best release first, then the rest as fallbacks, capped so a show
	// with no live seeders anywhere fails in a reasonable time.
	out := []score.Result{*found.Best}
	for _, r := range found.Results {
		if len(out) >= maxAttempts {
			break
		}
		if r.Torrent.InfoHash != found.Best.Torrent.InfoHash && r.AutoPick {
			out = append(out, r)
		}
	}
	return out, nil
}

const maxAttempts = 4

// NoRelease explains why nothing could be played. RawOnly is singled out: a
// just-aired episode exists as a raw capture hours before anyone subs it.
type NoRelease struct {
	Episode  int
	Queries  int
	Found    int
	RawOnly  bool
	RawTitle string
	message  string
}

func (e *NoRelease) Error() string { return e.message }

func noRelease(found Candidates, req PlayRequest) error {
	out := &NoRelease{
		Episode: req.Episode,
		Queries: len(found.Queries),
		Found:   len(found.Results),
	}

	for _, r := range found.Results {
		if r.RejectedOnlyAsRaw() {
			out.RawOnly = true
			out.RawTitle = r.Torrent.Title
			break
		}
	}

	switch {
	case out.RawOnly:
		out.message = fmt.Sprintf(
			"episode %d is out as a raw broadcast, but no subtitled release yet — "+
				"subs usually follow within a few hours", req.Episode)
	case out.Found == 0:
		out.message = fmt.Sprintf(
			"no release found for episode %d — the search covered %d title variant(s)",
			req.Episode, out.Queries)
	default:
		out.message = fmt.Sprintf(
			"found %d release(s) for episode %d, but none met your quality or audio preferences — "+
				"pick one manually or relax the settings", out.Found, req.Episode)
	}
	return out
}

func (p *Playback) launch(ctx context.Context, req PlayRequest, s *Session) error {
	opts := player.Options{
		URL:     s.StreamURL,
		Title:   s.Title,
		StartAt: s.StartAt,
		Audio:   req.Prefs.Audio,
	}
	// mpv can open the file directly; going back out through HTTP would add a
	// loopback copy of every byte for no benefit.
	if s.LocalPath != "" {
		opts.URL = s.LocalPath
	}

	// The browser pins when its stream opens; mpv has no stream session, so this
	// is the only place that knows an episode is now being watched.
	if s.InfoHash != "" {
		if err := p.store.PinOnly(ctx, s.InfoHash, s.FileIndex); err != nil {
			p.log.Warn("pin playing episode", "anime", req.AnimeID, "err", err)
		}
	}

	prefs, _ := p.store.Prefs(ctx, req.AnimeID)
	opts.Anime4K = prefs.Bool("playback.anime4k")
	opts.Anime4KMode = prefs.String("playback.anime4k_mode")
	opts.Anime4KSize = prefs.String("playback.anime4k_size")

	// mpv only auto-skips from these, so they have to be resolved here.
	if ranges := p.skipRanges(ctx, req, opts.URL); len(ranges) > 0 {
		opts.SkipRanges = ranges
		opts.AutoSkip = true

		path := filepath.Join(p.cacheDir, fmt.Sprintf("chapters-%d-%d.ini", req.AnimeID, req.Episode))
		if err := player.WriteChapters(path, ranges, 0); err == nil {
			opts.ChaptersFile = path
		}
	}

	if err := p.player.Play(ctx, opts); err != nil {
		return fmt.Errorf("start mpv: %w", err)
	}
	go p.track(req.AnimeID, req.Episode, req.Season, p.trackGen.Add(1))
	return nil
}

// skipRanges resolves the opening and ending for mpv: the file's own chapters
// first, then AniSkip, which needs the real length the probe gives.
func (p *Playback) skipRanges(ctx context.Context, req PlayRequest, source string) []player.SkipRange {
	var chapters []player.SkipRange
	var duration int

	if p.prober != nil && source != "" {
		probeCtx, cancel := context.WithTimeout(ctx, probeDeadline)
		info, err := p.prober.Probe(probeCtx, source)
		cancel()
		if err != nil {
			p.log.Debug("probe for skip times", "anime", req.AnimeID, "err", err)
		} else {
			duration = int(info.Duration)
			for _, c := range info.Chapters {
				if c.Kind != "" {
					chapters = append(chapters, player.SkipRange{Kind: c.Kind, Start: c.Start, End: c.End})
				}
			}
		}
	}

	if hasKind(chapters, "op") && hasKind(chapters, "ed") {
		return chapters
	}
	if p.enricher != nil {
		if err := p.enricher.Skips(ctx, req.AnimeID, req.Episode, duration); err != nil {
			p.log.Warn("fetch skip times", "anime", req.AnimeID, "err", err)
		}
	}

	stored, err := p.store.SkipRanges(ctx, req.AnimeID, req.Episode)
	if err != nil {
		return chapters
	}
	for _, r := range stored {
		if !hasKind(chapters, r.Kind) {
			chapters = append(chapters, r)
		}
	}
	return chapters
}

func hasKind(ranges []player.SkipRange, kind string) bool {
	for _, r := range ranges {
		if r.Kind == kind {
			return true
		}
	}
	return false
}

func (p *Playback) track(animeID, episode, season int, gen uint64) {
	const saveEvery = 5 * time.Second

	var last time.Time
	var reported bool

	// Media time actually played since the last save. mpv reports position ~1/s,
	// so a jump larger than a few seconds is a seek and does not count.
	var played, lastPos float64
	const maxStep = 5.0

	for ev := range p.player.Events() {
		// A newer episode owns the player now; this one must not write its
		// positions under the previous episode's number.
		if p.trackGen.Load() != gen {
			return
		}
		switch ev.Kind {
		case player.EventPosition:
			if step := ev.Position - lastPos; step > 0 && step <= maxStep {
				played += step
			}
			lastPos = ev.Position

			if time.Since(last) < saveEvery {
				continue
			}
			last = time.Now()
			if p.save(animeID, episode, ev, played) && !reported {
				reported = true
				p.watched(animeID, episode)
			}
			played = 0

		case player.EventEnd, player.EventExit:
			// End and exit events carry no position, so the last one seen is the
			// resume point; without this every mpv session ended by saving zero.
			if ev.Position == 0 {
				ev.Position = lastPos
			}
			// The threshold can be crossed within the last save interval before a
			// quit, so the final save is the last chance to record it watched.
			if p.save(animeID, episode, ev, played) && !reported {
				reported = true
				p.watched(animeID, episode)
			}
			played = 0
			// A short episode can end before any position event crosses the
			// threshold, so completion is also a signal.
			if !reported && ev.Reason == "eof" {
				p.watched(animeID, episode)
			}
			p.unpin(animeID, episode)

			// Only a natural end advances. Closing the window is a decision
			// to stop, and starting the next episode would ignore it.
			if ev.Kind == player.EventEnd && ev.Reason == "eof" {
				p.advance(animeID, episode, season)
			}
			return
		}
	}
}

// advance plays the next episode when one exists. The prefetcher has usually
// already pulled it, so this starts almost immediately.
func (p *Playback) advance(animeID, episode, season int) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	prefs, err := p.store.Prefs(ctx, animeID)
	if err != nil || !prefs.Bool("playback.autonext") {
		return
	}

	next, err := p.store.NextEpisode(ctx, animeID, episode)
	if err != nil || next == 0 {
		return
	}

	p.log.Info("advancing to next episode", "anime", animeID, "episode", next)

	if _, err := p.Start(ctx, PlayRequest{
		AnimeID:  animeID,
		Episode:  next,
		Season:   season,
		External: true,
		Prefs:    prefsFromSettings(prefs),
	}); err != nil {
		p.log.Warn("auto-advance", "anime", animeID, "episode", next, "err", err)
	}
}

func (p *Playback) watched(animeID, episode int) {
	if p.sync == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := p.sync.Watched(ctx, animeID, episode); err != nil {
		p.log.Warn("record watched", "anime", animeID, "episode", episode, "err", err)
	}
	p.autoDelete(animeID)
}

func (p *Playback) autoDelete(animeID int) {
	if p.cache == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := p.cache.AutoDelete(ctx, animeID); err != nil {
		p.log.Warn("auto-delete watched", "anime", animeID, "err", err)
	}
}

// Releasing the pin returns the file to the cache manager's control.
func (p *Playback) unpin(animeID, episode int) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	hash, index, err := p.store.CachedFile(ctx, animeID, epKey(episode))
	if err != nil || hash == "" {
		return
	}
	if err := p.store.PinCache(ctx, hash, index, false); err != nil {
		p.log.Warn("unpin", "err", err)
	}
}

// save records a report and returns whether the store now calls the episode
// watched.
func (p *Playback) save(animeID, episode int, ev player.Event, played float64) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	watched, err := p.store.SavePlayback(ctx, store.PlaybackState{
		AnimeID:  animeID,
		EpKey:    epKey(episode),
		Position: ev.Position,
		Duration: ev.Duration,
		Played:   played,
	})
	if err != nil {
		p.log.Warn("save playback", "err", err)
		return false
	}
	return watched
}

func epKey(episode int) string { return fmt.Sprint(episode) }
