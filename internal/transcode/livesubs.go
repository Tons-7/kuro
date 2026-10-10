package transcode

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Live subtitles: a process beside each pass writes every text track's lines as it reads them.
// Not outputs of the pass itself: those left re-encoded audio far behind the picture, and nothing played.

func liveName(track, from int) string { return fmt.Sprintf("live-%d-%d.ass", track, from) }

// subtitleArgs is the whole command of the subtitle process for a pass, nil when there is nothing to write.
// Paced like the pass, so it reads the same part of a file still downloading.
func (s *Session) subtitleArgs(offset float64, from int) []string {
	outputs := s.liveSubtitleArgs(from)
	if len(outputs) == 0 {
		return nil
	}
	a := []string{"-hide_banner", "-loglevel", "error", "-nostdin"}
	if offset > 0 {
		a = append(a, "-ss", fmt.Sprintf("%.3f", offset))
	}
	a = append(a,
		"-readrate", fmt.Sprintf("%.1f", readRate),
		"-readrate_initial_burst", fmt.Sprintf("%.0f", initialBurstSeconds),
		"-i", s.Source,
		// The episode's own times, as in the pass: lines are placed by them.
		"-copyts",
	)
	return append(a, outputs...)
}

// liveSubtitleArgs is one output per text track. Called under the locks args is.
func (s *Session) liveSubtitleArgs(from int) []string {
	if !s.liveSubs || s.Info == nil {
		return nil
	}
	var a []string
	for _, sub := range s.Info.Subtitles {
		if !Renderable(sub.Codec) {
			continue
		}
		codec := "ass"
		if isASS(sub.Codec) {
			codec = "copy"
		}
		a = append(a,
			"-map", fmt.Sprintf("0:%d?", sub.Index), "-c:s", codec, "-f", "ass",
			// The muxer otherwise holds lines until earlier ones arrive, which after a seek is never.
			"-ignore_readorder", "1", "-flush_packets", "1",
			"-y", liveName(sub.Index, from),
		)
	}
	return a
}

// LiveSubtitles reports whether a running pass is writing subtitle lines, and where in the episode it began.
func (s *Session) LiveSubtitles() (start float64, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// A pass whose subtitle process died writes no lines; the slower reads have to find them.
	if !s.liveSubs || s.run == nil || !s.run.subsAlive.Load() {
		return 0, false
	}
	return float64(s.headFrom) * SegmentSeconds, true
}

// How long a first request waits for the subtitle process to open the file and write the track's header.
var liveHeaderWait = 5 * time.Second

// AwaitLive is the track as the subtitle process has it so far, lines or none, waiting briefly for its
// header: an empty track at once beats a 45-second read of the bytes that process is already reading.
func (s *Subtitles) AwaitLive(ctx context.Context, dir string, track int, codec string) (string, bool) {
	deadline := time.After(liveHeaderWait)
	for {
		if path, err := s.MergeLive(dir, track, codec); err == nil {
			if text, readErr := os.ReadFile(path); readErr == nil && strings.Contains(string(text), "[Events]") {
				return path, true
			}
		}
		select {
		case <-ctx.Done():
			return "", false
		case <-deadline:
			return "", false
		case <-time.After(150 * time.Millisecond):
		}
	}
}

// LiveCovers: a pass begun mid-episode drops lines that started before it, so it covers at only past those.
func LiveCovers(start, at float64) bool { return start == 0 || at >= start+aroundBefore }

// startSubtitles runs the subtitle process beside a pass. The picture never waits on it: failing to
// start, or dying, only means lines are read the slower way.
func (s *Session) startSubtitles(r *run) {
	args := s.subtitleArgs(float64(r.from)*SegmentSeconds, r.from)
	if args == nil {
		return
	}
	cmd := exec.Command(s.ffmpeg, args...)
	cmd.Dir = s.dir
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		s.log.Warn("subtitle process did not start", "session", s.ID, "err", err)
		return
	}
	r.subs = cmd
	r.subsAlive.Store(true)
	go func() {
		err := cmd.Wait()
		r.subsAlive.Store(false)
		// Killed with its pass is the ordinary end; only its own failure is worth a line.
		if err != nil && !r.subsStopped.Load() {
			s.log.Warn("subtitle process failed", "session", s.ID, "err", err,
				"detail", lastLines(stderr.String(), encoderLogLines))
		}
	}()
}

// stopSubtitles ends the subtitle process of a pass that was killed or failed. One that ended normally
// is left to finish: it may be a few lines behind the picture.
func (r *run) stopSubtitles() {
	if r.subs == nil || r.subs.Process == nil {
		return
	}
	r.subsStopped.Store(true)
	r.subs.Process.Kill()
}

// MergeLive folds what the encoder passes have written for a track into its file, and returns the path.
func (s *Subtitles) MergeLive(dir string, track int, codec string) (string, error) {
	path := filepath.Join(dir, fmt.Sprintf("sub-%d.%s", track, subtitleExt(codec)))
	lives, _ := filepath.Glob(filepath.Join(dir, fmt.Sprintf("live-%d-*.ass", track)))
	if len(lives) == 0 {
		return path, nil
	}

	one := s.lockFor(path)
	one.Lock()
	defer one.Unlock()

	have, _ := os.ReadFile(path)
	merged := string(have)
	for _, live := range lives {
		raw, err := os.ReadFile(live)
		if err != nil {
			continue
		}
		// The encoder is mid-write: a last line without its newline is not a line yet.
		text := string(raw)
		if i := strings.LastIndexByte(text, '\n'); i >= 0 {
			text = text[:i+1]
		} else {
			continue
		}
		merged = mergeASS(merged, text)
	}
	if merged == string(have) {
		return path, nil
	}
	// Aside and moved, as for a read: the track may be served this instant.
	staging := path + ".live"
	if err := os.WriteFile(staging, []byte(merged), 0o644); err != nil {
		return path, err
	}
	if err := os.Rename(staging, path); err != nil {
		os.Remove(staging)
		return path, err
	}
	return path, nil
}
