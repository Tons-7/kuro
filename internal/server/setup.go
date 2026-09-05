package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"kuro/internal/config"
	"kuro/internal/deps"
)

// Component is one external program kuro shells out to. None are bundled:
// together they exceed half a gigabyte, against a 25 MB application.
type Component struct {
	Name     string `json:"name"`
	Label    string `json:"label"`
	Purpose  string `json:"purpose"`
	Size     string `json:"size"`
	Required bool   `json:"required"`
	Present  bool   `json:"present"`
	Version  string `json:"version,omitempty"`
	// Latest is what is published, when known; newer than Version means an
	// update is on offer.
	Latest string `json:"latest,omitempty"`
	Needs  string `json:"needs,omitempty"`
	// Manual holds a package-manager command when kuro cannot fetch this
	// component on this OS, so the page guides instead of offering a download
	// that would fail.
	Manual string `json:"manual,omitempty"`
}

// setup reports what is installed so the first-run screen can say exactly what
// it wants to fetch and why, rather than downloading half a gigabyte unasked.
func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	versions := s.installedVersions()

	components := []Component{
		{
			Name: "rqbit", Label: "Torrent engine",
			Purpose:  "Streams episodes. Nothing plays from a torrent without it.",
			Size:     "10 MB",
			Required: true,
			Present:  s.hasBinary("rqbit"),
			Version:  versions["rqbit"],
		},
		{
			Name: "ffmpeg", Label: "Transcoder",
			Purpose: "Converts releases the browser cannot play. Needed to watch " +
				"in a browser, on a phone or on a TV.",
			Size:    "420 MB",
			Present: s.hasBinary("ffmpeg") && s.hasBinary("ffprobe"),
			Version: versions["ffmpeg"],
		},
		{
			Name: "mpv", Label: "Desktop player",
			Purpose: "Optional external player. Better performance than a browser, " +
				"but desktop only.",
			Size:    "115 MB",
			Present: s.hasBinary("mpv"),
			Version: versions["mpv"],
		},
		{
			Name: "anime4k", Label: "Anime4K shaders",
			Purpose: "Upscaling shaders for mpv. The browser player uses its own " +
				"WebGPU build and needs no download.",
			Size:    "1 MB",
			Present: s.hasShaders(),
			Version: versions["anime4k"],
			Needs:   "mpv",
		},
	}

	for i := range components {
		if !components[i].Present {
			components[i].Manual = deps.ManualCommand(components[i].Name)
		}
	}
	// Versions come from the network; the page gets what was last resolved
	// and a refresh runs behind it.
	if s.deps != nil {
		for i := range components {
			if !components[i].Present {
				continue
			}
			components[i].Latest = s.deps.LatestKnown(components[i].Name)
			if components[i].Latest == "" {
				go s.refreshLatest(components[i].Name)
			}
		}
	}

	cache, _ := s.store.SettingInt(r.Context(), "cache.budget_bytes")

	// A nil slice marshals to null; the first-run page reads .length off this, so
	// an empty slice is required rather than null.
	roots, _ := s.scannerRoots(r)
	if roots == nil {
		roots = []string{}
	}

	ready := true
	for _, c := range components {
		if c.Required && !c.Present {
			ready = false
		}
	}

	body := map[string]any{
		"components":   components,
		"ready":        ready,
		"indexers":     len(s.cfg.Indexers),
		"binDir":       s.cfg.BinDir,
		"cacheDir":     s.cfg.CacheDir,
		"cacheBudget":  cache,
		"libraryPaths": roots,
		"progress":     []any{},
	}
	if s.deps != nil {
		body["progress"] = s.deps.Status()
	}
	send(w, http.StatusOK, body)
}

// Long enough that a failing lookup cannot be retried on every poll.
const latestRetry = 10 * time.Minute

// refreshLatest resolves a component's newest version in the background. One
// at a time per component: the page polls every second, nothing is cached when
// the lookup fails, and the retries would otherwise pile up against the very
// rate limit that caused the failure.
func (s *Server) refreshLatest(name string) {
	s.latestMu.Lock()
	if s.latestJobs == nil {
		s.latestJobs = map[string]time.Time{}
	}
	if at, running := s.latestJobs[name]; running && time.Since(at) < latestRetry {
		s.latestMu.Unlock()
		return
	}
	s.latestJobs[name] = time.Now()
	s.latestMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := s.deps.Latest(ctx, name); err != nil {
		s.log.Debug("latest version", "component", name, "err", err)
	}
}

// installComponent starts a download and returns immediately: ffmpeg is 420 MB
// and the page polls setup for progress.
func (s *Server) installComponent(w http.ResponseWriter, r *http.Request) {
	if s.deps == nil {
		send(w, http.StatusServiceUnavailable, map[string]any{
			"error": "installer unavailable",
		})
		return
	}

	name := r.PathValue("name")
	if !deps.Known(name) {
		send(w, http.StatusNotFound, map[string]any{"error": "unknown component"})
		return
	}
	if err := s.deps.Install(name); err != nil {
		s.fail(w, "install component", err)
		return
	}
	send(w, http.StatusAccepted, map[string]any{"installing": name})
}

// hasBinary is true when kuro downloaded the tool into bin/ or the OS provides
// it on PATH (a Homebrew/apt install), which is how macOS and Linux supply the
// engines kuro cannot fetch itself.
func (s *Server) hasBinary(name string) bool {
	info, err := os.Stat(s.cfg.Path(config.ExeName(name)))
	if err == nil && !info.IsDir() && info.Size() > 0 {
		return true
	}
	_, err = exec.LookPath(config.ExeName(name))
	return err == nil
}

func (s *Server) hasShaders() bool {
	matches, err := filepath.Glob(filepath.Join(s.cfg.BinDir, "shaders", "*.glsl"))
	return err == nil && len(matches) > 10
}

func (s *Server) scannerRoots(r *http.Request) ([]string, error) {
	if s.scanner == nil {
		return nil, nil
	}
	return s.scanner.Roots(r.Context())
}

// installedVersions reads what fetch-deps recorded. Absent on a hand-assembled
// bin directory, which is not an error, just unknown.
func (s *Server) installedVersions() map[string]string {
	out := map[string]string{}

	raw, err := os.ReadFile(filepath.Join(s.cfg.BinDir, "versions.json"))
	if err != nil {
		return out
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]string{}
	}
	return out
}
