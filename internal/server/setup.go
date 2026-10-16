package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"kuro/internal/config"
	"kuro/internal/deps"
	"kuro/internal/indexer"
	"kuro/internal/player"
	"kuro/internal/torrent"
	"kuro/internal/transcode"
)

// Component is one external program kuro shells out to. None are bundled:
// together they exceed half a gigabyte, against a 25 MB application.
type Component struct {
	Name     string `json:"name"`
	Label    string `json:"label"`
	Purpose  string `json:"purpose"`
	Size     string `json:"size"`
	Required bool   `json:"required"`
	// Role replaces the "Optional" badge where that would mislead.
	Role    string `json:"role,omitempty"`
	Present bool   `json:"present"`
	Version string `json:"version,omitempty"`
	// Latest is what is published, when known; newer than Version means an update is on offer.
	Latest string `json:"latest,omitempty"`
	Needs  string `json:"needs,omitempty"`
	// Manual: the package-manager command, where kuro cannot fetch this component on this OS.
	Manual string `json:"manual,omitempty"`
	// System: found on PATH rather than downloaded into bin/.
	System bool `json:"system,omitempty"`
	// Problem says why one that is there cannot be used.
	Problem string `json:"problem,omitempty"`
}

// setup reports what is installed so the first-run screen can say exactly what
// it wants to fetch and why, rather than downloading half a gigabyte unasked.
func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	versions := s.installedVersions()

	components := []Component{
		{
			Name: "engine", Label: "Torrent engine",
			Purpose:  "Built into kuro: streams and downloads episodes. Nothing to install.",
			Required: true,
			Present:  true,
			Version:  "built in",
		},
		{
			Name: "ffmpeg", Label: "Transcoder",
			Purpose: "ffmpeg and ffprobe. Prepares every episode for the built-in player: in a " +
				"browser, on a phone or on a TV. Not needed if you only play in mpv or VLC.",
			Size:    "420 MB",
			Role:    "Needed for the built-in player",
			Present: s.hasBinary("ffmpeg") && s.hasBinary("ffprobe"),
			Problem: s.halfTranscoder(),
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
		if components[i].Name == "ffmpeg" && components[i].Present {
			s.describeSystemFFmpeg(r.Context(), &components[i])
		}
	}
	for i := range components {
		if !components[i].Present {
			components[i].Manual = deps.ManualCommand(components[i].Name)
		}
	}
	// Versions come from the network; the page gets what was last resolved and a refresh runs behind it.
	if s.deps != nil {
		for i := range components {
			// The built-in engine updates with kuro; a system install, with its package manager.
			if !components[i].Present || components[i].System || !deps.Known(components[i].Name) {
				continue
			}
			components[i].Latest = s.deps.LatestKnown(components[i].Name)
			if components[i].Latest == "" {
				go s.refreshLatest(components[i].Name)
			}
		}
	}

	cache, _ := s.store.SettingInt(r.Context(), "cache.budget_bytes")

	// Empty, not nil: the first-run page reads .length off this, and nil marshals to null.
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

	// Counted as usable, and the rest named: a bad block is skipped at startup.
	usable, bad := 0, []string{}
	for _, in := range s.cfg.Indexers {
		if _, err := indexer.Build(in.Type, in.URL, in.Adult); err != nil {
			bad = append(bad, err.Error())
		} else {
			usable++
		}
	}

	body := map[string]any{
		"components":     components,
		"ready":          ready,
		"indexers":       usable,
		"badIndexers":    bad,
		"defaultDataDir": config.DefaultDataDir(),
		"configPath":     s.cfg.ConfigPath(),
		"dataDir":        s.cfg.DataDir(),
		"strayConfig":    s.cfg.StrayConfig(),
		"vlc":            player.ResolveVLC(s.cfg.VLCPath()),
		"temporary":      s.cfg.Temporary(),
		"binDir":         s.cfg.BinDir,
		"cacheDir":       s.cfg.CacheDir,
		"cacheBudget":    cache,
		"libraryPaths":   roots,
		"progress":       []any{},
	}
	if s.deps != nil {
		body["progress"] = s.deps.Status()
	}
	send(w, http.StatusOK, body)
}

// setDataDir writes the new folder to config.toml and copies the database there; empty means default.
func (s *Server) setDataDir(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path string `json:"path"`
		// UseExisting confirms switching to a database already in that folder.
		UseExisting bool `json:"useExisting"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&body); err != nil {
		send(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		return
	}
	dir := s.cfg.ResolveDataDir(strings.TrimSpace(body.Path))
	if dir == s.cfg.DataDir() {
		send(w, http.StatusOK, map[string]any{"dataDir": dir, "copied": false, "restart": false})
		return
	}
	// Opening another history instead of carrying this one is asked, not assumed.
	if info, err := os.Stat(filepath.Join(dir, "kuro.db")); err == nil && !body.UseExisting {
		send(w, http.StatusConflict, map[string]any{
			"error":    "that folder already holds a kuro database",
			"existing": true,
			"modified": info.ModTime().Unix(),
		})
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		send(w, http.StatusBadRequest, map[string]any{"error": "cannot create folder: " + err.Error()})
		return
	}
	probe, err := os.CreateTemp(dir, ".kuro-*")
	if err != nil {
		send(w, http.StatusBadRequest, map[string]any{"error": "folder is not writable"})
		return
	}
	probe.Close()
	os.Remove(probe.Name())

	// Only into an empty home: a database already there is someone's history.
	target := filepath.Join(dir, "kuro.db")
	copied := false
	if dir != s.cfg.DataDir() && !fileExists(target) && fileExists(s.cfg.DatabasePath()) {
		if err := s.store.SnapshotTo(r.Context(), target); err != nil {
			os.Remove(target)
			s.fail(w, "copy database", err)
			return
		}
		copied = true
	}
	if err := s.cfg.SetDataDir(strings.TrimSpace(body.Path)); err != nil {
		s.fail(w, "write config", err)
		return
	}
	// Restarted now: anything written to the old database after the copy would be lost.
	if s.relaunch != nil {
		go func() {
			time.Sleep(time.Second)
			s.relaunch()
		}()
	}
	send(w, http.StatusOK, map[string]any{"dataDir": dir, "copied": copied, "restart": s.relaunch != nil})
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// Long enough that a failing lookup cannot be retried on every poll.
const latestRetry = 10 * time.Minute

// refreshLatest looks up a component's newest version in the background, one lookup per component at a time.
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

// installComponent starts a download and returns at once; the page polls for progress.
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

type systemTool struct {
	modTime   time.Time
	version   string
	canEncode bool
}

// describeSystemFFmpeg reads a PATH ffmpeg's version; one that can't encode counts as missing.
func (s *Server) describeSystemFFmpeg(ctx context.Context, c *Component) {
	path := s.cfg.Tool("ffmpeg")
	if torrent.Within(s.cfg.BinDir, path) {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	s.systemMu.Lock()
	t, ok := s.systemFF[path]
	s.systemMu.Unlock()
	if !ok || !t.modTime.Equal(info.ModTime()) {
		probe, cancel := context.WithTimeout(ctx, time.Minute)
		t = systemTool{
			modTime:   info.ModTime(),
			version:   transcode.Version(probe, path),
			canEncode: transcode.CanEncode(probe, path),
		}
		cancel()
		s.systemMu.Lock()
		if s.systemFF == nil {
			s.systemFF = map[string]systemTool{}
		}
		s.systemFF[path] = t
		s.systemMu.Unlock()
	}
	c.System = true
	c.Version = t.version
	if !t.canEncode {
		c.Present = false
		c.Problem = "The ffmpeg on this system cannot encode H.264 (no libx264, no working hardware encoder)."
	}
}

// hasBinary: the tool is in bin/ or on PATH, as macOS and Linux supply what kuro can't fetch.
func (s *Server) hasBinary(name string) bool {
	info, err := os.Stat(s.cfg.Path(config.ExeName(name)))
	if err == nil && !info.IsDir() && info.Size() > 0 {
		return true
	}
	_, err = exec.LookPath(config.ExeName(name))
	return err == nil
}

// halfTranscoder explains a row that reads "not installed" to someone who has one of the pair.
func (s *Server) halfTranscoder() string {
	ffmpeg, ffprobe := s.hasBinary("ffmpeg"), s.hasBinary("ffprobe")
	switch {
	case ffmpeg && !ffprobe:
		return "ffmpeg is on this system but ffprobe is not; kuro needs both. Install fetches a matching pair."
	case ffprobe && !ffmpeg:
		return "ffprobe is on this system but ffmpeg is not; kuro needs both. Install fetches a matching pair."
	}
	return ""
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

// installedVersions reads what fetch-deps recorded; a hand-assembled bin has none, which is fine.
func (s *Server) installedVersions() map[string]string {
	out := map[string]string{}

	raw, err := os.ReadFile(filepath.Join(s.cfg.BinDir, "versions.json"))
	if err != nil {
		return out
	}
	raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]string{}
	}
	return out
}
