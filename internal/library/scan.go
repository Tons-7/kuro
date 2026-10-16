package library

import (
	"context"
	"encoding/json"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"kuro/internal/match"
	"kuro/internal/parse"
	"kuro/internal/store"
	"kuro/internal/torrent"
)

// Extras, trailers and samples are video too, so size separates an episode from them.
const minEpisodeBytes = 20 << 20

// Directories that hold anything but episodes.
var skipDir = map[string]bool{
	"extras": true, "specials.disabled": true, "sample": true,
	"samples": true, "trailers": true, "featurettes": true,
	"$recycle.bin": true, "system volume information": true,
	".git": true, "@eadir": true,
}

type Scanner struct {
	store *store.Store
	log   *slog.Logger
	// cacheDir is never walked: a part-downloaded torrent there is not a library file.
	cacheDir string

	mu   sync.Mutex
	last *ScanReport
}

// WithCacheDir names kuro's own download folder, which a library root may contain.
func (s *Scanner) WithCacheDir(dir string) *Scanner {
	if abs, err := filepath.Abs(dir); err == nil {
		s.cacheDir = abs
	}
	return s
}

// LastReport is the last scan's report, nil before any.
func (s *Scanner) LastReport() *ScanReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last
}

func NewScanner(s *store.Store, log *slog.Logger) *Scanner {
	return &Scanner{store: s, log: log}
}

type ScanReport struct {
	Roots     []string `json:"roots"`
	Seen      int      `json:"seen"`
	Video     int      `json:"video"`
	Matched   int      `json:"matched"`
	Unmatched int      `json:"unmatched"`
	Missing   int      `json:"missing"`
	Took      string   `json:"took"`
	Errors    []string `json:"errors,omitempty"`
}

// Roots reads the configured folders, a JSON array so a comma or semicolon can't split a path.
func (s *Scanner) Roots(ctx context.Context) ([]string, error) {
	raw, err := s.store.Setting(ctx, "library.paths")
	if err != nil || strings.TrimSpace(raw) == "" {
		return nil, err
	}

	var paths []string
	if err := json.Unmarshal([]byte(raw), &paths); err != nil {
		return nil, err
	}

	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, filepath.Clean(p))
		}
	}
	return out, nil
}

// Scan walks the configured roots and records every episode it can identify.
// Unmatched files are still stored so they can be assigned by hand.
func (s *Scanner) Scan(ctx context.Context, ix *match.Index) (ScanReport, error) {
	start := time.Now()

	roots, err := s.Roots(ctx)
	if err != nil {
		return ScanReport{}, err
	}
	rep := ScanReport{Roots: roots}
	if len(roots) == 0 {
		rep.Took = time.Since(start).Round(time.Millisecond).String()
		return rep, nil
	}

	boosts, err := s.store.MatchBoosts(ctx)
	if err != nil {
		s.log.Warn("match boosts", "err", err)
	}

	var found []store.LocalFile
	var walked, resolved []string
	for _, root := range roots {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		// Through a junction or symlink: WalkDir would see no folder and sweep everything under it.
		real := root
		if r, err := filepath.EvalSymlinks(root); err == nil {
			real = r
		}
		resolved = append(resolved, real)
		files, seen, failed, err := s.walk(ctx, real)
		rep.Seen += seen
		if err != nil {
			rep.Errors = append(rep.Errors, root+": "+err.Error())
			continue
		}
		found = append(found, files...)
		// A folder that could not be read this time keeps its rows: sweeping them would drop hand assignments.
		if len(failed) > 0 {
			for _, f := range failed {
				rep.Errors = append(rep.Errors, f+": not readable; its files were left as they were")
			}
			continue
		}
		walked = append(walked, real)
	}
	rep.Video = len(found)

	for i := range found {
		if identify(&found[i], ix, boosts) {
			rep.Matched++
		} else {
			rep.Unmatched++
		}
	}

	// One stamp per scan, so anything older is exactly what wasn't seen.
	stamp, err := s.store.NextScanStamp(ctx)
	if err != nil {
		return rep, err
	}
	if _, err := s.store.SaveLocalFiles(ctx, stamp, found); err != nil {
		return rep, err
	}
	// Only the roots that walked: one that failed contributed no files, and
	// sweeping it would mark an unplugged drive's whole library missing.
	if rep.Missing, err = s.store.SweepLocal(ctx, stamp, walked); err != nil {
		return rep, err
	}
	// Rows under no configured root: a folder taken out of the library takes its files with it.
	if outside, err := s.store.SweepOutside(ctx, resolved); err != nil {
		return rep, err
	} else {
		rep.Missing += outside
	}
	s.mu.Lock()
	s.last = &rep
	s.mu.Unlock()

	rep.Took = time.Since(start).Round(time.Millisecond).String()
	s.log.Info("library scan", "roots", len(roots), "video", rep.Video,
		"matched", rep.Matched, "unmatched", rep.Unmatched,
		"missing", rep.Missing, "took", rep.Took)
	return rep, nil
}

// walk lists the videos under root; failed names the folders inside it that could not be read.
func (s *Scanner) walk(ctx context.Context, root string) (out []store.LocalFile, seen int, failed []string, err error) {
	visited := map[string]bool{}
	err = s.walkInto(ctx, root, root, visited, &out, &seen, &failed)
	return out, seen, failed, err
}

func (s *Scanner) walkInto(ctx context.Context, root, dir string, visited map[string]bool, out *[]store.LocalFile, seen *int, failed *[]string) error {
	if visited[dir] {
		return nil
	}
	visited[dir] = true
	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// A root that cannot be read is a configuration problem worth surfacing; a folder inside it is noted.
			if path == root {
				return err
			}
			s.log.Debug("skip path", "path", path, "err", err)
			if d != nil && d.IsDir() {
				*failed = append(*failed, path)
				return fs.SkipDir
			}
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Junctions and symlinks are walked where they point; a root one, via a trailing separator.
		if d.Type()&(fs.ModeSymlink|fs.ModeIrregular) != 0 {
			sep := string(filepath.Separator)
			if path == dir {
				if !strings.HasSuffix(dir, sep) {
					return s.walkInto(ctx, root, dir+sep, visited, out, seen, failed)
				}
				return nil
			}
			if target, err := filepath.EvalSymlinks(path); err == nil {
				if info, err := os.Stat(target); err == nil && info.IsDir() {
					return s.walkInto(ctx, root, target, visited, out, seen, failed)
				}
			}
			return nil
		}
		if d.IsDir() {
			name := strings.ToLower(d.Name())
			if skipDir[name] || strings.HasPrefix(name, ".") && path != dir {
				return fs.SkipDir
			}
			if s.cacheDir != "" && torrent.Within(s.cacheDir, path) {
				return fs.SkipDir
			}
			return nil
		}

		*seen++
		if !parse.VideoExt(strings.ToLower(filepath.Ext(path))) {
			return nil
		}

		info, err := d.Info()
		if err != nil || info.Size() < minEpisodeBytes {
			return nil
		}

		*out = append(*out, store.LocalFile{
			Path:     path,
			Size:     info.Size(),
			Modified: info.ModTime().Unix(),
		})
		return nil
	})
}

// bareEpisode reports a file named only for its episode number, like "05".
// The parser reads that as a title, which no index will ever match.
func bareEpisode(name string) (int, bool) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" || len(trimmed) > 4 {
		return 0, false
	}
	for _, r := range trimmed {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(trimmed)
	return n, err == nil && n > 0
}

// identify parses the file name, else its folder's, which is usually named for the show.
func identify(f *store.LocalFile, ix *match.Index, boosts map[int]float64) bool {
	name := strings.TrimSuffix(filepath.Base(f.Path), filepath.Ext(f.Path))
	rel := parse.Parse(name)

	f.Title = rel.Title
	f.Episode = rel.Episode
	f.Season = rel.Season
	f.Resolution = rel.Resolution
	f.Group = rel.Group

	if n, ok := bareEpisode(name); ok {
		f.Title, f.Episode = "", n
	}

	// The folder names what the file does not: the show, or the season of a bare "Show - 05".
	parent := parse.Parse(filepath.Base(filepath.Dir(f.Path)))
	if f.Title == "" && parent.Title != "" {
		f.Title = parent.Title
		if f.Episode == 0 {
			f.Episode = parent.Episode
		}
	}
	if f.Season == 0 {
		f.Season = parent.Season
	}
	if ix == nil || f.Title == "" {
		return false
	}

	// The download path's confidence threshold, so a weak guess waits for review.
	res, ok := ix.Match(match.Query{
		Title:   f.Title,
		Season:  f.Season,
		Episode: f.Episode,
		Boost:   boosts,
	})
	// A toss-up between seasons is left for a hand assignment, not played as whichever ranked first.
	if !ok || res.Ambiguous() {
		return false
	}

	f.AnimeID = res.MediaID
	// Below 1: confidence 1 is a hand assignment, which rescans never touch.
	f.Confidence = scanConfidence(res.Score)
	return true
}

func scanConfidence(score float64) float64 {
	return min(score/40, 0.99)
}
