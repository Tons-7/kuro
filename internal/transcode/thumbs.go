package transcode

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Scrub frames are laid out as one sprite sheet the player offsets into. One
// image avoids a round trip per hover and a hundred small files to clean up.
type Thumbnails struct {
	ffmpeg string
	log    *slog.Logger

	mu      sync.Mutex
	running map[string]struct{}
	// failed: when a sheet last failed, so the player polling every 15 s does not restart ffmpeg each time.
	failed map[string]time.Time
}

func NewThumbnails(ffmpeg string, log *slog.Logger) *Thumbnails {
	return &Thumbnails{
		ffmpeg:  absolute(ffmpeg),
		log:     log,
		running: map[string]struct{}{},
		failed:  map[string]time.Time{},
	}
}

// Sheet describes where each frame sits, so the client can turn a hovered time into a background offset.
type Sheet struct {
	Ready bool `json:"ready"`
	// Seconds between frames.
	Interval float64 `json:"interval"`
	Columns  int     `json:"columns"`
	Rows     int     `json:"rows"`
	Count    int     `json:"count"`
	Width    int     `json:"width"`
	Height   int     `json:"height"`
}

const (
	// Wide enough to make out a scene, small enough for the episode to fit in one image.
	thumbWidth = 160
	// About this many frames whatever the length, so episodes and films both scrub well.
	thumbTarget = 144
	// A sheet needs the whole episode, so only a complete file gets one.
	thumbDeadline = 5 * time.Minute
	// A failed sheet is tried again this much later, not on the next poll.
	thumbRetry = time.Hour
)

func sheetPath(dir string) string { return filepath.Join(dir, "thumbs.jpg") }

// sheetArgs decodes keyframes only: 160 px tiles need no more, and every frame of a film took longer than the deadline.
func sheetArgs(source, filter, part string) []string {
	return []string{"-hide_banner", "-loglevel", "error", "-y", "-skip_frame", "nokey", "-i", source,
		"-vf", filter, "-frames:v", "1", "-q:v", "6", part}
}

// tail keeps the end of ffmpeg's output, where it says what went wrong.
func tail(out string) string {
	const keep = 300
	out = strings.TrimSpace(out)
	if len(out) <= keep {
		return out
	}
	return out[len(out)-keep:]
}

// PlanSheet works out the grid for this length, for a handler describing a sheet it didn't build.
func PlanSheet(duration float64) Sheet {
	if duration <= 0 {
		return Sheet{}
	}

	interval := math.Max(2, math.Round(duration/thumbTarget))
	// A second short of the end: the container often outlasts the picture by a
	// subtitle or audio tail, and a frame sampled there came out black.
	count := max(1, int(math.Ceil((duration-1)/interval)))
	columns := int(math.Ceil(math.Sqrt(float64(count))))
	rows := int(math.Ceil(float64(count) / float64(columns)))

	return Sheet{
		Interval: interval,
		Columns:  columns,
		Rows:     rows,
		Count:    count,
		Width:    thumbWidth,
	}
}

// Read reports an existing sheet. Nothing is generated here: the player asks on
// every open and a missing sheet is the normal answer for a fresh episode.
func (t *Thumbnails) Read(dir string, duration float64, aspect float64) (Sheet, bool) {
	plan := PlanSheet(duration)
	if plan.Count == 0 {
		return Sheet{}, false
	}
	plan.Height = tileHeight(aspect)

	if _, err := os.Stat(sheetPath(dir)); err != nil {
		return plan, false
	}
	plan.Ready = true
	return plan, true
}

// tileHeight keeps the video's shape, rounded even as the encoder refuses odd dimensions.
func tileHeight(aspect float64) int {
	if aspect <= 0 {
		aspect = 16.0 / 9.0
	}
	h := int(math.Round(float64(thumbWidth) / aspect))
	if h%2 != 0 {
		h++
	}
	return h
}

// Generate builds the sheet once, in the background, from the file on disk (not through the engine).
func (t *Thumbnails) Generate(source, dir string, duration, aspect float64) {
	plan := PlanSheet(duration)
	if t == nil || plan.Count == 0 || source == "" {
		return
	}
	if _, err := os.Stat(sheetPath(dir)); err == nil {
		return
	}

	t.mu.Lock()
	if _, busy := t.running[dir]; busy || time.Since(t.failed[dir]) < thumbRetry {
		t.mu.Unlock()
		return
	}
	t.running[dir] = struct{}{}
	t.mu.Unlock()

	go func() {
		defer func() {
			t.mu.Lock()
			delete(t.running, dir)
			t.mu.Unlock()
		}()

		ctx, cancel := context.WithTimeout(context.Background(), thumbDeadline)
		defer cancel()

		// Written aside and renamed, so a half-finished sheet is never served.
		// The extension stays .jpg because ffmpeg picks its muxer from it.
		part := filepath.Join(dir, "thumbs.part.jpg")
		// tpad holds the last keyframe to the end: decoding keyframes only left the tiles after it black.
		filter := fmt.Sprintf("tpad=stop_mode=clone:stop_duration=%g,fps=1/%g,scale=%d:%d,tile=%dx%d",
			duration, plan.Interval, thumbWidth, tileHeight(aspect), plan.Columns, plan.Rows)

		started := time.Now()
		// ffmpeg complains about a downloading file's holes yet makes a sheet; only real failure is reported.
		out, err := exec.CommandContext(ctx, t.ffmpeg, sheetArgs(source, filter, part)...).CombinedOutput()
		if err != nil {
			t.log.Warn("scrub previews", "err", err, "detail", tail(string(out)))
			os.Remove(part)
			t.mu.Lock()
			t.failed[dir] = time.Now()
			t.mu.Unlock()
			return
		}

		if err := os.Rename(part, sheetPath(dir)); err != nil {
			t.log.Warn("scrub previews", "err", err)
			return
		}
		t.log.Info("scrub previews ready",
			"frames", plan.Count, "took", time.Since(started).Round(time.Second))
	}()
}
