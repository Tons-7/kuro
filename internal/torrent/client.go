// Package torrent drives rqbit, which runs as a sidecar process. Its streaming
// endpoint blocks until the requested pieces arrive; reading the file directly
// cannot, because a sparse region on NTFS reads as zeroes with no error.
package torrent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Client struct {
	http   *http.Client
	base   string
	engine Engine

	// A magnet whose metadata would not resolve is remembered so play does not
	// spend the inspect timeout on it again while the user retries. A swarm can
	// revive, so the mark expires.
	mu         sync.Mutex
	badMagnets map[string]time.Time
}

// Engine is what runs the sidecar this client talks to. Wiring one in makes
// calls start it on demand instead of failing when it is not up yet.
type Engine interface {
	Ensure(context.Context) error
	Down()
}

func (c *Client) WithEngine(e Engine) *Client {
	c.engine = e
	return c
}

// engineGone tells a dead engine from a caller that gave up or an engine that
// is merely slow; only the first should trigger a restart.
func engineGone(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var timeout interface{ Timeout() bool }
	if errors.As(err, &timeout) && timeout.Timeout() {
		return false
	}
	return true
}

// How long a dead magnet stays skipped. Long enough to spare a retry storm,
// short enough that a swarm coming back is picked up on the next attempt.
const inspectFailTTL = 3 * time.Minute

func NewClient(base string) *Client {
	return &Client{
		http:       &http.Client{Timeout: 60 * time.Second},
		base:       strings.TrimRight(base, "/"),
		badMagnets: map[string]time.Time{},
	}
}

type File struct {
	Name       string   `json:"name"`
	Components []string `json:"components"`
	Length     int64    `json:"length"`
	Included   bool     `json:"included"`
}

type Torrent struct {
	ID      int    `json:"id"`
	Details Detail `json:"details"`
}

type Detail struct {
	InfoHash     string `json:"info_hash"`
	Name         string `json:"name"`
	OutputFolder string `json:"output_folder"`
	Files        []File `json:"files"`
}

type Stats struct {
	State         string `json:"state"`
	Error         string `json:"error"`
	ProgressBytes int64  `json:"progress_bytes"`
	TotalBytes    int64  `json:"total_bytes"`
	Finished      bool   `json:"finished"`
	Live          *struct {
		DownloadSpeed struct {
			Mbps float64 `json:"mbps"`
		} `json:"download_speed"`
		Snapshot struct {
			PeerStats struct {
				Live int `json:"live"`
			} `json:"peer_stats"`
		} `json:"snapshot"`
	} `json:"live"`
}

// Peers is how many are actually connected. A tracker's seeder count is
// routinely wrong.
func (s Stats) Peers() int {
	if s.Live == nil {
		return 0
	}
	return s.Live.Snapshot.PeerStats.Live
}

func (s Stats) Percent() float64 {
	if s.TotalBytes == 0 {
		return 0
	}
	return float64(s.ProgressBytes) / float64(s.TotalBytes) * 100
}

// How long a magnet gets to produce its file list. A live swarm answers in
// seconds, a dead one never does — short so a dead release doesn't stall the queue.
const inspectTimeout = 25 * time.Second

// Inspect fetches the file list without committing to a download, so the
// caller can pick which file is the episode before anything is written. A
// magnet that recently failed to resolve is refused at once, so a retry does
// not pay the timeout again on a release that already proved undeliverable.
func (c *Client) Inspect(parent context.Context, magnet string) (*Torrent, error) {
	if until, bad := c.magnetFailedRecently(magnet); bad {
		return nil, fmt.Errorf("metadata did not resolve; skipping for %s", time.Until(until).Round(time.Second))
	}

	ctx, cancel := context.WithTimeout(parent, inspectTimeout)
	defer cancel()

	got, err := c.add(ctx, magnet, url.Values{"list_only": {"true"}})
	// A caller cancelling says nothing about the swarm; marking it here made
	// the next "try again" fail at once.
	if err != nil && parent.Err() == nil {
		c.markMagnetFailed(magnet)
	}
	return got, err
}

func (c *Client) magnetFailedRecently(magnet string) (time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	until, ok := c.badMagnets[magnet]
	if ok && time.Now().Before(until) {
		return until, true
	}
	if ok {
		delete(c.badMagnets, magnet)
	}
	return time.Time{}, false
}

func (c *Client) markMagnetFailed(magnet string) {
	c.mu.Lock()
	c.badMagnets[magnet] = time.Now().Add(inspectFailTTL)
	c.mu.Unlock()
}

// Add downloads exactly one file. A season batch has a dozen episodes in it
// and only the requested one should ever touch the disk.
func (c *Client) Add(ctx context.Context, magnet string, file File) (*Torrent, error) {
	return c.add(ctx, magnet, url.Values{
		"overwrite":        {"true"},
		"only_files_regex": {"^" + regexp.QuoteMeta(file.Name) + "$"},
	})
}

func (c *Client) add(ctx context.Context, magnet string, params url.Values) (*Torrent, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.base+"/torrents?"+params.Encode(), strings.NewReader(magnet))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "text/plain")

	var out Torrent
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Details reports where the torrent's files landed, so their real cost on
// disk can be measured rather than inferred from download progress.
func (c *Client) Details(ctx context.Context, id int) (Detail, error) {
	var d Detail
	err := c.get(ctx, fmt.Sprintf("/torrents/%d", id), &d)
	return d, err
}

func (c *Client) Stats(ctx context.Context, id int) (Stats, error) {
	var s Stats
	err := c.get(ctx, fmt.Sprintf("/torrents/%d/stats/v1", id), &s)
	return s, err
}

// StreamURL is handed straight to mpv or ffmpeg. rqbit serves it with Range
// support and moves its priority window to wherever the player seeks.
func (c *Client) StreamURL(id, fileIndex int) string {
	return fmt.Sprintf("%s/torrents/%d/stream/%d", c.base, id, fileIndex)
}

// WaitLive blocks until the torrent leaves the initializing state; streaming
// before then fails with "invalid state: initializing".
func (c *Client) WaitLive(ctx context.Context, id int, timeout time.Duration) error {
	until := time.Now().Add(timeout)
	var resumed bool
	var verified int64

	for {
		stats, err := c.Stats(ctx, id)
		switch {
		case err != nil:
			// Transient during startup; keep trying until the deadline.
		case stats.Error != "":
			return fmt.Errorf("torrent %d: %s", id, stats.Error)
		case stats.State == "live" || stats.Finished:
			return nil

		// After a restart rqbit re-checks part-downloaded files and answers stream
		// reads with "invalid state: initializing"; extend the deadline while
		// progress climbs so a swarm timeout doesn't expire on local disk work.
		case stats.State == "initializing" && stats.ProgressBytes > verified:
			verified = stats.ProgressBytes
			until = time.Now().Add(timeout)

		// A paused torrent never goes live on its own; pauses come from the queue
		// or startup, never from a decision about this episode, so resume it.
		case stats.State == "paused" && !resumed:
			resumed = true
			if err := c.Start(ctx, id); err != nil {
				return fmt.Errorf("torrent %d is paused and would not start: %w", id, err)
			}
		}

		if time.Now().After(until) {
			return fmt.Errorf("torrent %d not live within %s", id, timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// Prewarm fetches the first and last bytes of the file before playback starts.
// mkvmerge writes the Matroska seek index at the tail, so without it scrubbing breaks.
func (c *Client) Prewarm(ctx context.Context, id, fileIndex int, window int64) error {
	if window <= 0 {
		window = 2 << 20
	}
	if err := c.WaitLive(ctx, id, 60*time.Second); err != nil {
		return err
	}
	stream := c.StreamURL(id, fileIndex)

	// Tail first, head last: each range request moves rqbit's priority window,
	// and it should end up where playback begins.
	for _, spec := range []string{
		fmt.Sprintf("bytes=-%d", window),
		fmt.Sprintf("bytes=0-%d", window-1),
	} {
		if err := c.readRange(ctx, stream, spec, window); err != nil {
			return err
		}
	}
	return nil
}

// PrewarmHead fetches only what playback starts from. Unlike Prewarm it skips
// the tail seek index, which isn't worth minutes of spinner on a slow line.
func (c *Client) PrewarmHead(ctx context.Context, id, fileIndex int, window int64) error {
	if window <= 0 {
		window = 2 << 20
	}
	if err := c.WaitLive(ctx, id, 60*time.Second); err != nil {
		return err
	}
	return c.readRange(ctx, c.StreamURL(id, fileIndex),
		fmt.Sprintf("bytes=0-%d", window-1), window)
}

// A ranged read is also how rqbit is told what to fetch next, so the bytes are
// discarded but the request is the point.
func (c *Client) readRange(ctx context.Context, stream, spec string, window int64) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, stream, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Range", spec)

	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("prewarm %s: %w", spec, err)
	}
	defer res.Body.Close()
	io.Copy(io.Discard, io.LimitReader(res.Body, window))
	return nil
}

// PrioritiseAt moves the download window; a ranged read is the only way to say
// "fetch here next". A fraction rather than bytes because the caller knows
// seconds, and constant bitrate lands close enough.
func (c *Client) PrioritiseAt(ctx context.Context, id, fileIndex int, fraction float64) error {
	if fraction <= 0 || fraction >= 1 {
		return nil
	}

	stats, err := c.Stats(ctx, id)
	if err != nil {
		return err
	}
	if stats.TotalBytes <= 0 {
		return nil
	}

	const window = 2 << 20
	offset := int64(float64(stats.TotalBytes) * fraction)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.StreamURL(id, fileIndex), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", offset, offset+window-1))

	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	io.Copy(io.Discard, io.LimitReader(res.Body, window))
	return nil
}

// ErrPaused separates a download someone stopped on purpose from one that
// broke: the queue drops the first and reports the second.
var ErrPaused = errors.New("download paused")

// How often Await asks rqbit where it is. Overridden in tests.
var awaitPoll = 5 * time.Second

// Await blocks until the torrent has everything it was asked for. It gives up
// once nothing has arrived for stall, so one dead release can't hold up the queue.
func (c *Client) Await(ctx context.Context, id int, stall time.Duration) error {
	var best int64
	var quiet time.Duration

	for {
		stats, err := c.Stats(ctx, id)
		switch {
		case err != nil:
			return err
		case stats.Finished:
			return nil
		case stats.Error != "":
			return fmt.Errorf("%s", stats.Error)
		case stats.State == "paused":
			return ErrPaused
		}

		if stats.ProgressBytes > best {
			best, quiet = stats.ProgressBytes, 0
		} else {
			quiet += awaitPoll
			if quiet >= stall {
				return fmt.Errorf("stalled at %.0f%%", stats.Percent())
			}
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(awaitPoll):
		}
	}
}

// Checking reports rqbit re-verifying a file after a launch; progress climbs
// from zero and pausing it freezes the check part way.
func (s Stats) Checking() bool { return s.State == "initializing" && !s.Finished }

// PauseUnfinished stops every part-downloaded torrent at startup: rqbit resumes
// its whole session on launch, so without this the queue downloads all at once.
// keep exempts a torrent; ones still checking are counted for a later pass.
func (c *Client) PauseUnfinished(ctx context.Context, keep func(infoHash string) bool) (paused, kept, checking int, err error) {
	list, err := c.List(ctx)
	if err != nil {
		return 0, 0, 0, err
	}

	for _, t := range list.Torrents {
		stats, err := c.Stats(ctx, t.ID)
		if err != nil || stats.Finished || stats.State == "paused" {
			continue
		}
		if stats.Checking() {
			checking++
			continue
		}
		if keep != nil && keep(strings.ToLower(t.InfoHash)) {
			kept++
			continue
		}
		if err := c.Pause(ctx, t.ID); err == nil {
			paused++
		}
	}
	return paused, kept, checking, nil
}

// Delete removes the torrent and its data; Forget keeps the files on disk.
func (c *Client) Delete(ctx context.Context, id int) error {
	return c.post(ctx, fmt.Sprintf("/torrents/%d/delete", id))
}

func (c *Client) Forget(ctx context.Context, id int) error {
	return c.post(ctx, fmt.Sprintf("/torrents/%d/forget", id))
}

// Asking for a state the torrent is already in is the outcome the caller wanted,
// not a failure — a button pressed against a slightly stale view shouldn't error.
func alreadyThere(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "already live") || strings.Contains(msg, "already paused")
}

func (c *Client) Pause(ctx context.Context, id int) error {
	err := c.post(ctx, fmt.Sprintf("/torrents/%d/pause", id))
	if alreadyThere(err) {
		return nil
	}
	return err
}

func (c *Client) Start(ctx context.Context, id int) error {
	err := c.post(ctx, fmt.Sprintf("/torrents/%d/start", id))
	if alreadyThere(err) {
		return nil
	}
	return err
}

type listing struct {
	Torrents []struct {
		ID       int    `json:"id"`
		InfoHash string `json:"info_hash"`
		Name     string `json:"name"`
	} `json:"torrents"`
}

func (c *Client) List(ctx context.Context) (listing, error) {
	var out listing
	err := c.get(ctx, "/torrents", &out)
	return out, err
}

// Live maps info hash to the id rqbit is currently using. Ids are per session
// and reused, so one recorded before a restart may name a different torrent.
func (c *Client) Live(ctx context.Context) (map[string]int, error) {
	list, err := c.List(ctx)
	if err != nil {
		return nil, err
	}

	out := make(map[string]int, len(list.Torrents))
	for _, t := range list.Torrents {
		if t.InfoHash != "" {
			out[strings.ToLower(t.InfoHash)] = t.ID
		}
	}
	return out, nil
}

// Ready reports whether the sidecar is accepting requests yet.
func (c *Client) Ready(ctx context.Context) bool {
	_, err := c.List(ctx)
	return err == nil
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return err
	}
	return c.do(req, out)
}

func (c *Client) post(ctx context.Context, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, nil)
	if err != nil {
		return err
	}
	return c.do(req, nil)
}

func (c *Client) do(req *http.Request, out any) error {
	if c.engine != nil {
		if err := c.engine.Ensure(req.Context()); err != nil {
			return err
		}
	}

	res, err := c.http.Do(req)
	if err != nil {
		if c.engine != nil && engineGone(err) {
			c.engine.Down()
		}
		return err
	}
	defer res.Body.Close()

	body, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return err
	}

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("rqbit %s: HTTP %d: %s",
			req.URL.Path, res.StatusCode, strings.TrimSpace(string(body)))
	}
	if out == nil || len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	return json.Unmarshal(body, out)
}

// PickVideo chooses the largest video file, which is correct for a single
// release: batches also carry samples, subtitles and artwork.
func PickVideo(files []File) (File, int, bool) {
	best, bestIdx := File{}, -1
	for i, f := range files {
		if !isVideo(f.Name) {
			continue
		}
		if bestIdx == -1 || f.Length > best.Length {
			best, bestIdx = f, i
		}
	}
	return best, bestIdx, bestIdx >= 0
}

// PickEpisode finds one episode inside a season pack by the number in each
// filename; only files well below the median size are ruled out as extras.
// numbers are every count the file may carry for it — the entry's own, a cour
// counted through the whole season, absolute — the first being the one asked for.
func PickEpisode(files []File, numbers ...int) (File, int, bool) {
	episode := 0
	if len(numbers) > 0 {
		episode = numbers[0]
	}
	if episode <= 0 {
		return PickVideo(files)
	}

	wanted := func(n int) bool { return n > 0 && slices.Contains(numbers, n) }

	var videos []int
	for i, f := range files {
		if isVideo(f.Name) {
			videos = append(videos, i)
		}
	}
	if len(videos) == 0 {
		return File{}, -1, false
	}

	// A lone unnumbered file is only acceptable for a one-episode entry, else
	// a film can play as a bogus episode number.
	if len(videos) == 1 {
		only := files[videos[0]]
		got := episodeInName(baseName(only.Name))
		if wanted(got) || episode <= 1 {
			return only, videos[0], true
		}
		return File{}, -1, false
	}

	floor := medianLength(files, videos) / 4

	for _, i := range videos {
		if files[i].Length < floor {
			continue
		}
		if wanted(episodeInName(baseName(files[i].Name))) {
			return files[i], i, true
		}
	}
	return File{}, -1, false
}

func medianLength(files []File, idx []int) int64 {
	lengths := make([]int64, 0, len(idx))
	for _, i := range idx {
		lengths = append(lengths, files[i].Length)
	}
	slices.Sort(lengths)
	return lengths[len(lengths)/2]
}

// Resolution, year and CRC checksums are all numbers in a filename, so the
// bracketed and tagged parts are stripped before looking for the episode.
var (
	episodeTags = regexp.MustCompile(`(?i)[\[\(][^\]\)]*[\]\)]|\b\d{3,4}[pi]\b|\bx?26[45]\b`)
	// A revision suffix is glued on ("05v2"), leaving no word boundary.
	versionSuffix = regexp.MustCompile(`(?i)(\d{1,4})v\d\b`)
	episodeNum    = regexp.MustCompile(`(?i)(?:\bS\d{1,2}E(\d{1,4})\b|(?:^|[\s_.-])(\d{1,4})(?:[\s_.-]|$))`)
)

func episodeInName(name string) int {
	cleaned := episodeTags.ReplaceAllString(name, " ")
	cleaned = versionSuffix.ReplaceAllString(cleaned, "$1 ")

	matches := episodeNum.FindAllStringSubmatch(cleaned, -1)
	for i := len(matches) - 1; i >= 0; i-- {
		for _, group := range matches[i][1:] {
			if group == "" {
				continue
			}
			n, err := strconv.Atoi(group)
			if err != nil || n <= 0 || n >= 5000 {
				continue
			}
			// A 4-digit year like "Title.2022.1080p" is not an episode number.
			if len(group) == 4 && n >= 1900 && n <= 2099 {
				continue
			}
			return n
		}
	}
	return 0
}

func baseName(path string) string {
	path = strings.ReplaceAll(path, `\`, "/")
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		path = path[i+1:]
	}
	if i := strings.LastIndexByte(path, '.'); i > 0 {
		path = path[:i]
	}
	return path
}

func isVideo(name string) bool {
	lower := strings.ToLower(name)
	for _, ext := range []string{".mkv", ".mp4", ".avi", ".ts", ".m2ts", ".webm", ".mov"} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}
