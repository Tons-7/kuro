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
}

func NewThumbnails(ffmpeg string, log *slog.Logger) *Thumbnails {
	return &Thumbnails{
		ffmpeg:  absolute(ffmpeg),
		log:     log,
		running: map[string]struct{}{},
	}
}

// Sheet describes where each frame sits, so the client can turn a hovered time
// into a background offset.
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
	// Wide enough to make out a scene, small enough that the whole episode fits
	// in one image a browser will decode without complaint.
	thumbWidth = 160
	// Around this many frames per episode whatever its length, so a 24-minute
	// episode and a two-hour film both give a usable scrub.
	thumbTarget = 144
	// A sheet is only worth making from a complete file, and ffmpeg has to walk
	// the whole episode to build one.
	thumbDeadline = 5 * time.Minute
)

func sheetPath(dir string) string { return filepath.Join(dir, "thumbs.jpg") }

// tail keeps the end of ffmpeg's output, which is where it says what went
// wrong; the start is a page of warnings about holes in the file.
func tail(out string) string {
	const keep = 300
	out = strings.TrimSpace(out)
	if len(out) <= keep {
		return out
	}
	return out[len(out)-keep:]
}

// PlanSheet works out the grid for an episode of this length. Exported for the
// handler, which has to describe a sheet it did not build.
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

// tileHeight keeps the tiles the shape of the video, rounded to an even number
// because the encoder refuses odd dimensions.
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

// Generate builds the sheet in the background, once. The source must be the
// file on disk: sampling across an episode through the torrent engine would
// fetch the whole thing.
func (t *Thumbnails) Generate(source, dir string, duration, aspect float64) {
	plan := PlanSheet(duration)
	if t == nil || plan.Count == 0 || source == "" {
		return
	}
	if _, err := os.Stat(sheetPath(dir)); err == nil {
		return
	}

	t.mu.Lock()
	if _, busy := t.running[dir]; busy {
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
		filter := fmt.Sprintf("fps=1/%g,scale=%d:%d,tile=%dx%d",
			plan.Interval, thumbWidth, tileHeight(aspect), plan.Columns, plan.Rows)

		started := time.Now()
		// A still-downloading file is full of holes ffmpeg complains about while
		// still producing a usable sheet, so its output is only reported on actual
		// failure.
		out, err := exec.CommandContext(ctx, t.ffmpeg,
			"-hide_banner", "-loglevel", "error", "-y",
			"-i", source,
			"-vf", filter,
			"-frames:v", "1",
			"-q:v", "6",
			part,
		).CombinedOutput()
		if err != nil {
			t.log.Warn("scrub previews", "err", err, "detail", tail(string(out)))
			os.Remove(part)
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
