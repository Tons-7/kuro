package transcode

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// A track reads as far as the download reached; the rest comes next request, so no longer wait.
const subtitleDeadline = 45 * time.Second

// How long a track from a still-downloading file is trusted before being read again.
const subtitleRefresh = 8 * time.Second

// Subtitles render in the browser, not burned in, which costs a GPU trip per frame and can't switch.
type Subtitles struct {
	ffmpeg string

	mu sync.Mutex
	// Tracks read from a source that won't grow; a downloading one would freeze at its first hole.
	done map[string]struct{}
	// One extraction per output path, or two runs writing one file hand back half of it.
	busy map[string]*sync.Mutex
}

// Font extraction sets its working directory to the output folder, so the path must be absolute.
func NewSubtitles(ffmpeg string) *Subtitles {
	return &Subtitles{
		ffmpeg: absolute(ffmpeg),
		done:   map[string]struct{}{},
		busy:   map[string]*sync.Mutex{},
	}
}

// lockFor serialises extractions of one track without blocking others.
func (s *Subtitles) lockFor(path string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()

	if m, ok := s.busy[path]; ok {
		return m
	}
	m := &sync.Mutex{}
	s.busy[path] = m
	return m
}

// Forget drops what is remembered about a closed session's folder, or both maps
// grow by every track of every episode for the life of the process.
func (s *Subtitles) Forget(dir string) {
	prefix := filepath.Clean(dir) + string(filepath.Separator)
	s.mu.Lock()
	defer s.mu.Unlock()
	for path := range s.done {
		if strings.HasPrefix(path, prefix) {
			delete(s.done, path)
		}
	}
	for path, m := range s.busy {
		// One in use keeps its lock; the next Forget takes it.
		if strings.HasPrefix(path, prefix) && m.TryLock() {
			m.Unlock()
			delete(s.busy, path)
		}
	}
}

type Track struct {
	Index    int    `json:"index"`
	Language string `json:"language"`
	Title    string `json:"title"`
	Codec    string `json:"codec"`
	Default  bool   `json:"default"`
	Forced   bool   `json:"forced"`
	URL      string `json:"url"`
}

type FontFile struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// Cues is how many lines the extracted track holds. Zero means the file was
// read but nothing recovered, which is a different problem from failing.
func (s *Subtitles) Cues(dir string, track int, codec string) int {
	return dialogueLines(filepath.Join(dir, fmt.Sprintf("sub-%d.%s", track, subtitleExt(codec))))
}

// Extract reads a track as ASS; unless complete, it is reread past subtitleRefresh, keeping the fuller read.
func (s *Subtitles) Extract(ctx context.Context, source, dir string, track int, codec string, complete bool) (string, error) {
	name := fmt.Sprintf("sub-%d.%s", track, subtitleExt(codec))
	path := filepath.Join(dir, name)

	one := s.lockFor(path)
	one.Lock()
	defer one.Unlock()

	// Checked in the lock, and the file too: closing a session deletes the directory under it.
	s.mu.Lock()
	_, already := s.done[path]
	s.mu.Unlock()
	if already {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
		s.mu.Lock()
		delete(s.done, path)
		s.mu.Unlock()
	}

	// Throttled only once the track holds cues; an empty read must be retried.
	if !complete {
		if info, err := os.Stat(path); err == nil && time.Since(info.ModTime()) < subtitleRefresh && dialogueLines(path) > 0 {
			return path, nil
		}
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(ctx, subtitleDeadline)
	defer cancel()

	args := []string{
		"-hide_banner", "-loglevel", "error", "-nostdin",
		"-i", source,
		"-map", fmt.Sprintf("0:%d", track),
	}
	// ASS is copied verbatim so its positioning and karaoke survive intact.
	if isASS(codec) {
		args = append(args, "-c:s", "copy")
	} else {
		args = append(args, "-c:s", "ass")
	}
	// Written aside and moved into place; the format is named, or ffmpeg infers it from ".part".
	staging := path + ".part"
	args = append(args, "-f", subtitleExt(codec), "-y", staging)

	out, err := exec.CommandContext(ctx, s.ffmpeg, args...).CombinedOutput()

	// Cues are interleaved through the file, so a track reads only as far as the
	// download reached. Partial output covers the part that can be watched.
	cues := dialogueLines(staging)
	have := dialogueLines(path)
	if err != nil && cues == 0 && have == 0 {
		os.Remove(staging)
		return "", fmt.Errorf("extract subtitle %d: %w: %s", track, err, strings.TrimSpace(string(out)))
	}

	// Holes move as pieces land, so reads are unioned and nothing read is lost.
	if have > 0 {
		if mergeErr := mergeInto(staging, path); mergeErr != nil {
			os.Remove(staging)
			return path, nil
		}
	}

	if renameErr := os.Rename(staging, path); renameErr != nil {
		os.Remove(staging)
		// The previous track is being served this instant; it is what there is.
		if have > 0 {
			return path, nil
		}
		return "", fmt.Errorf("install subtitle %d: %w", track, renameErr)
	}

	if err == nil && complete {
		s.mu.Lock()
		s.done[path] = struct{}{}
		s.mu.Unlock()
	}
	return path, nil
}

// A playhead read covers a little before, for the line on screen, and minutes ahead.
const (
	aroundBefore = 30.0
	aroundSpan   = 330.0
	aroundLimit  = 20 * time.Second
)

// ExtractAround merges in the track near the playhead, which is downloaded where the middle may not be.
func (s *Subtitles) ExtractAround(ctx context.Context, source, dir string, track int, codec string, at float64) (string, error) {
	path := filepath.Join(dir, fmt.Sprintf("sub-%d.%s", track, subtitleExt(codec)))

	one := s.lockFor(path)
	one.Lock()
	defer one.Unlock()

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, aroundLimit)
	defer cancel()

	from := max(0, at-aroundBefore)
	args := []string{
		"-hide_banner", "-loglevel", "error", "-nostdin",
		"-ss", strconv.FormatFloat(from, 'f', 3, 64), "-t", strconv.FormatFloat(aroundSpan, 'f', 0, 64),
		// Times as in the file, not from the seek point, or the merge misplaces them.
		"-copyts",
		"-i", source,
		"-map", fmt.Sprintf("0:%d", track),
	}
	if isASS(codec) {
		args = append(args, "-c:s", "copy")
	} else {
		args = append(args, "-c:s", "ass")
	}
	staging := path + ".around"
	args = append(args, "-f", subtitleExt(codec), "-y", staging)
	out, err := exec.CommandContext(ctx, s.ffmpeg, args...).CombinedOutput()
	defer os.Remove(staging)

	if dialogueLines(staging) == 0 {
		if err != nil {
			return path, fmt.Errorf("extract subtitle %d around %.0fs: %w: %s", track, at, err, strings.TrimSpace(string(out)))
		}
		return path, nil
	}
	if _, statErr := os.Stat(path); statErr == nil {
		if err := mergeInto(staging, path); err != nil {
			return path, err
		}
	}
	// Aside and moved, as for a full read: the track may be served this instant.
	return path, os.Rename(staging, path)
}

// mergeInto rewrites read as the union of itself and the track at have.
func mergeInto(read, have string) error {
	a, err := os.ReadFile(have)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(read)
	if err != nil {
		return err
	}
	return os.WriteFile(read, []byte(mergeASS(string(a), string(b))), 0o644)
}

// dialogueLines counts cues; everything is written as ASS, so one shape fits.
func dialogueLines(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()

	var n int
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for scan.Scan() {
		if strings.HasPrefix(scan.Text(), "Dialogue:") {
			n++
		}
	}
	return n
}

// ExtractFonts writes the container's font attachments to disk. Without them
// the renderer substitutes fonts and styled signs come out wrong.
func (s *Subtitles) ExtractFonts(ctx context.Context, source, dir string, attachments []Attachment) ([]FontFile, error) {
	fontDir := filepath.Join(dir, "fonts")

	if entries, err := os.ReadDir(fontDir); err == nil && len(entries) > 0 {
		return listFonts(fontDir), nil
	}
	if len(attachments) == 0 {
		return nil, nil
	}
	if err := os.MkdirAll(fontDir, 0o755); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	// By index, to our own names: the stored ones are the release maker's.
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin"}
	used := map[string]bool{}
	for i, a := range attachments {
		name := safeFontName(a.Filename, i)
		for used[strings.ToLower(name)] {
			name = fmt.Sprintf("%d-%s", i, name)
		}
		used[strings.ToLower(name)] = true
		args = append(args, fmt.Sprintf("-dump_attachment:%d", a.Index), filepath.Join(fontDir, name))
	}
	// -t 0 stops after the header; without it ffmpeg decodes the whole file.
	args = append(args, "-i", source, "-t", "0", "-f", "null", "-")

	// Non-zero is normal here: there is no output stream to write.
	_ = exec.CommandContext(ctx, s.ffmpeg, args...).Run()

	return listFonts(fontDir), nil
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._ -]+`)

// safeFontName keeps the stored name's base and nothing that leaves the folder.
func safeFontName(stored string, i int) string {
	base := filepath.Base(strings.ReplaceAll(stored, `\`, "/"))
	base = strings.Trim(unsafeName.ReplaceAllString(base, "_"), ". ")
	if base == "" || base == "_" {
		return fmt.Sprintf("font%d.ttf", i)
	}
	return base
}

func listFonts(dir string) []FontFile {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var out []FontFile
	for _, e := range entries {
		if e.IsDir() || !isFont(e.Name()) {
			continue
		}
		out = append(out, FontFile{Name: e.Name()})
	}
	return out
}

func isFont(name string) bool {
	lower := strings.ToLower(name)
	for _, ext := range []string{".ttf", ".otf", ".ttc", ".woff", ".woff2"} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

// Bitmaps are named rather than the text formats, which are an open list: one
// missing from a whitelist is a show whose only track silently disappears.
func isBitmapSubtitle(codec string) bool {
	switch codec {
	case "hdmv_pgs_subtitle", "pgssub", "dvd_subtitle", "dvdsub",
		"dvb_subtitle", "dvbsub", "xsub", "hdmv_text_subtitle":
		return true
	}
	return false
}

func isTextSubtitle(codec string) bool {
	return codec != "" && !isBitmapSubtitle(codec)
}

func isASS(codec string) bool { return codec == "ass" || codec == "ssa" }

// Names what was written, not what the track started as.
func subtitleExt(codec string) string {
	if isTextSubtitle(codec) {
		return "ass"
	}
	return "sup"
}

// Renderable reports whether the browser renderer can draw the track.
func Renderable(codec string) bool { return isTextSubtitle(codec) }
