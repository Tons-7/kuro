// Package torrent is the engine's loopback client; its stream waits for pieces, where a sparse file reads zeroes.
package torrent

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"kuro/internal/parse"
)

type Client struct {
	http *http.Client
	// No timeouts: stream reads, even headers, take as long as the swarm; the caller's context bounds them.
	stream *http.Client
	base   string
	engine Engine

	// Magnets whose metadata never resolved, so a retry skips the timeout; a swarm can revive, so it expires.
	mu         sync.Mutex
	badMagnets map[string]time.Time
	secret     string
}

// Engine is what this client talks to; wired in, calls start it on demand.
type Engine interface {
	Ensure(context.Context) error
}

func (c *Client) WithEngine(e Engine) *Client {
	c.engine = e
	return c
}

// SecretHeader carries the per-run secret the engine wants on every request that is not a stream.
const SecretHeader = "X-Kuro-Engine"

// NewSecret is a random token for one run.
func NewSecret() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (c *Client) WithSecret(secret string) *Client {
	c.secret = secret
	return c
}

// Addr is the engine's host:port.
func (c *Client) Addr() string {
	if u, err := url.Parse(c.base); err == nil {
		return u.Host
	}
	return ""
}

func (c *Client) url(path string) string { return c.base + path }

// ErrUnavailable wraps every unreachable engine, so "no engine" differs from "the engine said no".
var ErrUnavailable = errors.New("torrent engine unavailable")

// Within reports whether path is dir or inside it.
func Within(dir, path string) bool {
	if dir == "" || path == "" {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// How long a dead magnet stays skipped. Long enough to spare a retry storm,
// short enough that a swarm coming back is picked up on the next attempt.
const inspectFailTTL = 3 * time.Minute

func NewClient(base string) *Client {
	return &Client{
		http:       &http.Client{Timeout: 60 * time.Second},
		stream:     &http.Client{},
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
	// Per file, in the order of Detail.Files.
	FileProgress []int64 `json:"file_progress"`
	Live         *Live   `json:"live"`
}

// Live is present while the torrent is downloading.
type Live struct {
	DownloadSpeed struct {
		// MiB/s, despite the name.
		Mbps float64 `json:"mbps"`
	} `json:"download_speed"`
	Snapshot struct {
		// Includes unverified bytes, so it moves mid-piece.
		FetchedBytes int64 `json:"fetched_bytes"`
		PeerStats    struct {
			Live int `json:"live"`
		} `json:"peer_stats"`
	} `json:"snapshot"`
}

// Peers is how many are actually connected. A tracker's seeder count is routinely wrong.
func (s Stats) Peers() int {
	if s.Live == nil {
		return 0
	}
	return s.Live.Snapshot.PeerStats.Live
}

// Received counts every byte arrived, verified or not.
func (s Stats) Received() int64 {
	if s.Live == nil {
		return s.ProgressBytes
	}
	return max(s.ProgressBytes, s.Live.Snapshot.FetchedBytes)
}

// FileDone falls back to the torrent's state without per-file progress.
func (s Stats) FileDone(d Detail, i int) bool {
	if i < 0 || i >= len(d.Files) || i >= len(s.FileProgress) {
		return s.Finished
	}
	return d.Files[i].Length > 0 && s.FileProgress[i] >= d.Files[i].Length
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

// Inspect fetches the file list before any download; a magnet that just failed to resolve is refused at once.
func (c *Client) Inspect(parent context.Context, magnet string) (*Torrent, error) {
	if until, bad := c.magnetFailedRecently(magnet); bad {
		return nil, fmt.Errorf("metadata did not resolve; skipping for %s", time.Until(until).Round(time.Second))
	}

	ctx, cancel := context.WithTimeout(parent, inspectTimeout)
	defer cancel()

	got, err := c.add(ctx, magnet, url.Values{"list_only": {"true"}})
	// A caller cancelling says nothing about the swarm, so it isn't marked.
	if err != nil && parent.Err() == nil {
		c.markMagnetFailed(magnet)
	}
	return got, err
}

// A release picked by hand has no next candidate to fall back on, so its file list is waited for.
const pickedInspectTimeout = 3 * time.Minute

// InspectPicked is Inspect for a release someone chose: a longer wait, and never refused for an earlier failure.
func (c *Client) InspectPicked(parent context.Context, magnet string) (*Torrent, error) {
	ctx, cancel := context.WithTimeout(parent, pickedInspectTimeout)
	defer cancel()
	return c.add(ctx, magnet, url.Values{"list_only": {"true"}})
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

// Add downloads exactly one file of a pack; one already held keeps its other selected files.
func (c *Client) Add(ctx context.Context, magnet string, file File) (*Torrent, error) {
	return c.add(ctx, magnet, url.Values{
		"overwrite":        {"true"},
		"only_files_regex": {"^" + regexp.QuoteMeta(file.Name) + "$"},
	})
}

func (c *Client) add(ctx context.Context, magnet string, params url.Values) (*Torrent, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.url("/torrents?"+params.Encode()), strings.NewReader(magnet))
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

// Details reports where the files landed, so their disk cost is measured, not inferred.
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

// StreamURL goes to mpv or ffmpeg; ranged reads make the engine fetch there first.
func (c *Client) StreamURL(id, fileIndex int) string {
	return c.url(fmt.Sprintf("/torrents/%d/stream/%d", id, fileIndex))
}

// WaitLive blocks until the torrent is past "initializing", before which streaming fails.
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

		// While the engine re-checks files ("initializing"), the deadline extends as progress climbs.
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

	// Tail first, head last: the head is what playback reads first.
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

func (c *Client) streamClient() *http.Client {
	if c.stream != nil {
		return c.stream
	}
	return http.DefaultClient
}

// A ranged read tells the engine what to fetch next; the bytes are discarded.
func (c *Client) readRange(ctx context.Context, stream, spec string, window int64) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, stream, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Range", spec)

	res, err := c.streamClient().Do(req)
	if err != nil {
		return fmt.Errorf("prewarm %s: %w", spec, err)
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return fmt.Errorf("prewarm %s: engine answered %s", spec, res.Status)
	}
	// Headers come before any byte; only a full read proves delivery.
	want := window
	if res.ContentLength >= 0 && res.ContentLength < want {
		want = res.ContentLength
	}
	n, err := io.Copy(io.Discard, io.LimitReader(res.Body, window))
	if err != nil {
		return fmt.Errorf("prewarm %s: %w", spec, err)
	}
	if n < want {
		return fmt.Errorf("prewarm %s: got %d of %d bytes", spec, n, want)
	}
	return nil
}

// PrioritiseAt moves the download window with a ranged read, at a fraction since callers know seconds.
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

	res, err := c.streamClient().Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	io.Copy(io.Discard, io.LimitReader(res.Body, window))
	return nil
}

// ErrPaused marks a download stopped on purpose, which the queue drops instead of reporting.
var ErrPaused = errors.New("download paused")

// How often Await asks the engine where it is. Overridden in tests.
var AwaitPoll = 5 * time.Second

// StallError is Await giving up: nothing arrived for Quiet, with Percent of the file on disk.
type StallError struct {
	Percent float64
	Quiet   time.Duration
}

func (e *StallError) Error() string { return fmt.Sprintf("stalled at %.0f%%", e.Percent) }

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
			quiet += AwaitPoll
			if quiet >= stall {
				return &StallError{Percent: stats.Percent(), Quiet: quiet}
			}
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(AwaitPoll):
		}
	}
}

// Checking reports the engine verifying data on disk; progress climbs from zero.
func (s Stats) Checking() bool { return s.State == "initializing" && !s.Finished }

// PauseUnfinished stops part-downloaded torrents the engine resumed on launch, except keep's.
func (c *Client) PauseUnfinished(ctx context.Context, keep func(infoHash string) bool) (paused, kept int, checking []string, err error) {
	list, err := c.List(ctx)
	if err != nil {
		return 0, 0, nil, err
	}

	for _, t := range list.Torrents {
		stats, err := c.Stats(ctx, t.ID)
		if err != nil || stats.Finished || stats.State == "paused" {
			continue
		}
		if stats.Checking() {
			checking = append(checking, strings.ToLower(t.InfoHash))
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

// Live maps info hash to the engine's current id, which changes per session.
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

// Ready reports whether the engine is accepting requests.
func (c *Client) Ready(ctx context.Context) bool {
	_, err := c.List(ctx)
	return err == nil
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url(path), nil)
	if err != nil {
		return err
	}
	return c.do(req, out)
}

func (c *Client) post(ctx context.Context, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url(path), nil)
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
	if c.secret != "" {
		req.Header.Set(SecretHeader, c.secret)
	}

	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	body, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return err
	}

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("engine %s: HTTP %d: %s",
			req.URL.Path, res.StatusCode, strings.TrimSpace(string(body)))
	}
	if out == nil || len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	return json.Unmarshal(body, out)
}

// PickVideo chooses the largest video file, right for a single release among samples and extras.
func PickVideo(files []File) (File, int, bool) {
	best, bestIdx := File{}, -1
	for i, f := range files {
		if !parse.IsVideo(f.Name) {
			continue
		}
		if bestIdx == -1 || f.Length > best.Length {
			best, bestIdx = f, i
		}
	}
	return best, bestIdx, bestIdx >= 0
}

// PickEpisode finds an episode in a pack by filename number; numbers are every count it may carry, own first.
func PickEpisode(files []File, numbers ...int) (File, int, bool) {
	if len(numbers) == 0 {
		return PickVideo(files)
	}
	return PickNumbered(files, numbers[0], numbers)
}

// PickNumbered is PickEpisode knowing the episode asked, so it reads a pack's count ("12" or "23").
func PickNumbered(files []File, episode int, numbers []int) (File, int, bool) {
	numbers = slices.DeleteFunc(slices.Clone(numbers), func(n int) bool { return n <= 0 })
	if episode <= 0 && len(numbers) > 0 {
		episode = numbers[0]
	}
	if episode <= 0 {
		return PickVideo(files)
	}

	var videos []int
	for i, f := range files {
		if parse.IsVideo(f.Name) && !extra(f.Name) {
			videos = append(videos, i)
		}
	}
	if len(videos) == 0 {
		return File{}, -1, false
	}

	// A lone unnumbered file only for a one-episode entry, or a film plays as an episode.
	if len(videos) == 1 {
		only := files[videos[0]]
		got := episodeInName(parse.BaseName(only.Name))
		if slices.Contains(numbers, got) || (got == 0 && episode <= 1) {
			return only, videos[0], true
		}
		return File{}, -1, false
	}

	floor := medianLength(files, videos) / 4
	numbered := map[int]int{}
	lowest, highest := 0, 0
	for _, i := range videos {
		if files[i].Length < floor {
			continue
		}
		if n := episodeInName(parse.BaseName(files[i].Name)); n > 0 {
			numbered[i] = n
			if lowest == 0 || n < lowest {
				lowest = n
			}
			highest = max(highest, n)
		}
	}
	want := packNumber(episode, numbers, lowest, highest)

	best := -1
	for _, i := range videos {
		if numbered[i] == want && (best < 0 || files[i].Length > files[best].Length) {
			best = i
		}
	}
	if best < 0 {
		return File{}, -1, false
	}
	return files[best], best, true
}

// packNumber: of the counts the episode may carry, one the pack holds; of several, the one starting nearest 1.
func packNumber(episode int, numbers []int, lowest, highest int) int {
	if len(numbers) == 0 {
		return 0
	}
	held := slices.DeleteFunc(slices.Clone(numbers), func(n int) bool { return n < lowest || n > highest })
	if len(held) == 1 {
		return held[0]
	}
	if len(held) == 0 {
		return numbers[0]
	}
	best, distance := held[0], -1
	for _, n := range held {
		first := n - (episode - 1)
		d := max(first-lowest, lowest-first)
		if distance < 0 || d < distance {
			best, distance = n, d
		}
	}
	return best
}

// extra: in an extras folder, or named as an opening, ending, preview, commercial or menu.
func extra(path string) bool {
	path = strings.ReplaceAll(path, `\`, "/")
	dirs := strings.Split(path, "/")
	for _, d := range dirs[:len(dirs)-1] {
		if extraDirs[strings.ToLower(d)] || strings.HasPrefix(strings.ToLower(d), "nc") {
			return true
		}
	}
	return extraName.MatchString(parse.BaseName(path))
}

var (
	extraDirs = map[string]bool{"extras": true, "extra": true, "sample": true, "samples": true, "trailers": true,
		"trailer": true, "featurettes": true, "specials": true, "sp": true, "menu": true, "menus": true, "bonus": true,
		"cm": true, "pv": true}
	extraName = regexp.MustCompile(`(?i)(?:^|[\s_.\-\[(])(?:NC(?:OP|ED)|OP|ED|PV|CM|Menu|Preview|Trailer|Sample)\s*\d{0,2}(?:$|[\s_.\-\]\)v])`)
)

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
	// "[05]", "[05v2]": the episode in a bracket of its own, read before the tags go.
	bracketNum = regexp.MustCompile(`\[(\d{1,4})(?:v\d)?\]`)
	// A revision suffix is glued on ("05v2"), leaving no word boundary.
	versionSuffix = regexp.MustCompile(`(?i)(\d{1,4})v\d\b`)
	episodeNum    = regexp.MustCompile(`(?i)(?:\bS\d{1,2}E(\d{1,4})\b|(?:^|[\s_.-])(\d{1,4})(?:[\s_.-]|$))`)
	seasonEpisode = regexp.MustCompile(`(?i)\bS\d{1,2}[\s._]?E(\d{1,4})\b`)
	// "E05", "EP05", "Ep. 5": the marked form without a season.
	markedEpisode = regexp.MustCompile(`(?i)(?:^|[\s._\-\[(])E(?:P|PISODE)?\.?\s*(\d{1,4})(?:v\d)?\b`)
	// "Show - 11 - 511 Kinderheim": the dashed number, not the title's.
	dashed = regexp.MustCompile(`[\s_]-[\s_]+(\d{1,4})(?:v\d)?(?:[\s_]|$)`)
	// "FLAC.2.0", "DDP5.1": read as numbers, every file of a pack was episode 2.
	audioLayout = regexp.MustCompile(`(?i)(?:^|[\s_.-]|[a-z])[2567]\.[01](?:[\s_.-]|$)`)
	// "12.5": a recap between two episodes, not episode 12.
	fractional = regexp.MustCompile(`(?:^|[\s_-])\d{1,4}\.\d(?:[\s_-]|$)`)
)

func episodeInName(name string) int {
	if m := bracketNum.FindStringSubmatch(name); m != nil {
		if n, _ := strconv.Atoi(m[1]); n > 0 && !(len(m[1]) == 4 && n >= 1900 && n <= 2099) {
			return n
		}
	}
	cleaned := episodeTags.ReplaceAllString(name, " ")
	// SxxEyy wins over any other number.
	if m := seasonEpisode.FindStringSubmatch(cleaned); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil && n > 0 {
			return n
		}
	}
	if m := markedEpisode.FindStringSubmatch(cleaned); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil && n > 0 {
			return n
		}
	}
	cleaned = audioLayout.ReplaceAllString(cleaned, " ")
	if fractional.MatchString(cleaned) {
		return 0
	}
	if m := dashed.FindStringSubmatch(cleaned); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil && n > 0 && !(len(m[1]) == 4 && n >= 1900 && n <= 2099) {
			return n
		}
	}
	cleaned = versionSuffix.ReplaceAllString(cleaned, "$1 ")

	matches := episodeNum.FindAllStringSubmatch(cleaned, -1)
	for _, match := range slices.Backward(matches) {
		for _, group := range match[1:] {
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

// ByteRange is a stretch of a file, [From, To) in bytes.
type ByteRange struct {
	From int64 `json:"from"`
	To   int64 `json:"to"`
}

// FileRanges is what has arrived of one file: its length and the complete stretches, in order.
type FileRanges struct {
	Length int64       `json:"length"`
	Ranges []ByteRange `json:"ranges"`
}

// Ranges reports the stretches of a file the engine has verified on disk.
func (c *Client) Ranges(ctx context.Context, id, file int) (FileRanges, error) {
	var out FileRanges
	err := c.get(ctx, fmt.Sprintf("/torrents/%d/files/%d/ranges", id, file), &out)
	return out, err
}
