package library

import (
	"context"
	"encoding/json"
	"io/fs"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"kuro/internal/match"
	"kuro/internal/parse"
	"kuro/internal/store"
)

// Extras, trailers and the sample files that ship inside release folders are
// all video, so size is what separates an episode from them.
const minEpisodeBytes = 20 << 20

var videoExt = map[string]bool{
	".mkv": true, ".mp4": true, ".avi": true, ".m4v": true,
	".mov": true, ".webm": true, ".ts": true, ".ogm": true, ".wmv": true,
}

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

// Roots reads the configured directories. The setting holds a JSON array so a
// path containing a comma or a semicolon cannot split it.
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
	var walked []string
	for _, root := range roots {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		files, seen, err := s.walk(ctx, root)
		rep.Seen += seen
		if err != nil {
			rep.Errors = append(rep.Errors, root+": "+err.Error())
			continue
		}
		found = append(found, files...)
		walked = append(walked, root)
	}
	rep.Video = len(found)

	for i := range found {
		if identify(&found[i], ix, boosts) {
			rep.Matched++
		} else {
			rep.Unmatched++
		}
	}

	// Every row this scan writes carries the same stamp, so anything older is
	// exactly what was not seen this time round.
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

	rep.Took = time.Since(start).Round(time.Millisecond).String()
	s.log.Info("library scan", "roots", len(roots), "video", rep.Video,
		"matched", rep.Matched, "unmatched", rep.Unmatched,
		"missing", rep.Missing, "took", rep.Took)
	return rep, nil
}

func (s *Scanner) walk(ctx context.Context, root string) ([]store.LocalFile, int, error) {
	var out []store.LocalFile
	var seen int

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// A root that cannot be read is a configuration problem worth
			// surfacing; one bad directory inside it is not.
			if path == root {
				return err
			}
			s.log.Debug("skip path", "path", path, "err", err)
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if d.IsDir() {
			name := strings.ToLower(d.Name())
			if skipDir[name] || strings.HasPrefix(name, ".") && path != root {
				return fs.SkipDir
			}
			return nil
		}

		seen++
		if !videoExt[strings.ToLower(filepath.Ext(path))] {
			return nil
		}

		info, err := d.Info()
		if err != nil || info.Size() < minEpisodeBytes {
			return nil
		}

		out = append(out, store.LocalFile{
			Path:     path,
			Size:     info.Size(),
			Modified: info.ModTime().Unix(),
		})
		return nil
	})
	return out, seen, err
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

// identify parses the file name and, failing that, the folder it sits in.
// Release folders are usually named for the show.
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

	if f.Title == "" {
		parent := parse.Parse(filepath.Base(filepath.Dir(f.Path)))
		if parent.Title != "" {
			f.Title = parent.Title
			if f.Season == 0 {
				f.Season = parent.Season
			}
			if f.Episode == 0 {
				f.Episode = parent.Episode
			}
		}
	}
	if ix == nil || f.Title == "" {
		return false
	}

	// Match applies the same confidence threshold as the download path, so a weak
	// guess is left for review rather than recorded as fact.
	res, ok := ix.Match(match.Query{
		Title:   f.Title,
		Season:  f.Season,
		Episode: f.Episode,
		Boost:   boosts,
	})
	if !ok {
		return false
	}

	f.AnimeID = res.MediaID
	f.Confidence = res.Score
	return true
}
