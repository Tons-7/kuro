package transcode

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Live subtitles: the pass encoding the picture also writes each text track's lines as it reads them.

func liveName(track, from int) string { return fmt.Sprintf("live-%d-%d.ass", track, from) }

// liveSubtitleArgs adds one more output per text track. Called from args, under its locks.
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
	if !s.liveSubs || s.run == nil {
		return 0, false
	}
	return float64(s.headFrom) * SegmentSeconds, true
}

// LiveCovers: a pass begun mid-episode drops lines that started before it, so it covers at only past those.
func LiveCovers(start, at float64) bool { return start == 0 || at >= start+aroundBefore }

// liveSubsFailed spots a pass killed by its subtitle output, so the picture can go on without it.
func liveSubsFailed(detail string) bool {
	// detail joins stderr lines with " | "; a lost input also fails to close every output, which is not this.
	for line := range strings.SplitSeq(strings.ToLower(detail), " | ") {
		about := strings.Contains(line, "live-") || strings.Contains(line, "subtitle") || strings.Contains(line, "[ass @")
		broke := strings.Contains(line, "error") || strings.Contains(line, "fail") || strings.Contains(line, "could not") || strings.Contains(line, "invalid")
		if about && broke && !strings.Contains(line, "trailer") && !strings.Contains(line, "closing") {
			return true
		}
	}
	return false
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
