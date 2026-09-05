// Package deps downloads the programs kuro shells out to. Over half a gigabyte
// total, so fetched on demand rather than bundled; same sources as
// scripts/fetch-deps.ps1 for identical binaries.
package deps

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

type Release struct {
	Version string
	URL     string
	Archive bool
	// Lowercase hex SHA-256, where the source publishes one. These downloads
	// are executed, so an unverified one is worth refusing.
	Digest string
}

type component struct {
	name    string
	files   []string
	glob    string
	into    string
	resolve func(context.Context, *Manager) (Release, error)
}

// Files are per-OS: an executable is ffmpeg.exe on Windows and ffmpeg
// elsewhere, and mpv ships an extra console shim only on Windows.
var components = []component{
	{name: "rqbit", files: componentFiles("rqbit"), resolve: resolveRqbit},
	{name: "ffmpeg", files: componentFiles("ffmpeg"), resolve: resolveFfmpeg},
	{name: "mpv", files: componentFiles("mpv"), resolve: resolveMpv},
	{name: "anime4k", glob: "*.glsl", into: "shaders", resolve: resolveAnime4K},
}

// Coarse on purpose: "downloading 180 of 420 MB" is what a waiter wants.
type Stage string

const (
	StageResolving   Stage = "resolving"
	StageDownloading Stage = "downloading"
	StageExtracting  Stage = "extracting"
	StageDone        Stage = "done"
	StageFailed      Stage = "failed"
)

type Progress struct {
	Component string `json:"component"`
	Stage     Stage  `json:"stage"`
	Version   string `json:"version,omitempty"`
	Bytes     int64  `json:"bytes"`
	Total     int64  `json:"total"`
	Error     string `json:"error,omitempty"`
}

type Manager struct {
	binDir string
	http   *http.Client
	log    *slog.Logger

	mu         sync.Mutex
	running    map[string]*Progress
	installed  func(name string)
	installing func(name string)
	latest     map[string]latestVersion
}

type latestVersion struct {
	version string
	at      time.Time
}

// OnInstalled is called with each component that lands, so what was detected
// at startup can be detected again instead of waiting for a restart.
func (m *Manager) OnInstalled(hook func(name string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.installed = hook
}

// OnInstalling is called before a download replaces a binary; a running one
// cannot be overwritten.
func (m *Manager) OnInstalling(hook func(name string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.installing = hook
}

// Latest is the newest version published for a component, remembered for an
// hour; the resolvers ask GitHub and the setup page asks often.
func (m *Manager) Latest(ctx context.Context, name string) (string, error) {
	m.mu.Lock()
	if v, ok := m.latest[name]; ok && time.Since(v.at) < time.Hour {
		m.mu.Unlock()
		return v.version, nil
	}
	m.mu.Unlock()

	var spec *component
	for i := range components {
		if components[i].name == name {
			spec = &components[i]
		}
	}
	if spec == nil {
		return "", fmt.Errorf("unknown component %q", name)
	}
	rel, err := spec.resolve(ctx, m)
	if err != nil {
		return "", err
	}

	m.mu.Lock()
	if m.latest == nil {
		m.latest = map[string]latestVersion{}
	}
	m.latest[name] = latestVersion{version: rel.Version, at: time.Now()}
	m.mu.Unlock()
	return rel.Version, nil
}

// LatestKnown is Latest without the network: what was last resolved, or "".
func (m *Manager) LatestKnown(name string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.latest[name].version
}

func New(binDir string, log *slog.Logger) *Manager {
	return &Manager{
		binDir:  binDir,
		http:    &http.Client{Timeout: 30 * time.Minute},
		log:     log,
		running: map[string]*Progress{},
	}
}

func Known(name string) bool {
	for _, c := range components {
		if c.name == name {
			return true
		}
	}
	return false
}

// Install fetches one component in the background. Asking for one already being
// fetched is not an error, so a double click does nothing twice.
func (m *Manager) Install(name string) error {
	var spec *component
	for i := range components {
		if components[i].name == name {
			spec = &components[i]
		}
	}
	if spec == nil {
		return fmt.Errorf("unknown component %q", name)
	}

	m.mu.Lock()
	if p, busy := m.running[name]; busy && p.Stage != StageDone && p.Stage != StageFailed {
		m.mu.Unlock()
		return nil
	}
	m.running[name] = &Progress{Component: name, Stage: StageResolving}
	m.mu.Unlock()

	go func() {
		// Detached from the request: half an installed ffmpeg is worse than
		// none, and the page that asked may be closed long before it lands.
		ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
		defer cancel()

		m.mu.Lock()
		before := m.installing
		m.mu.Unlock()
		if before != nil {
			before(name)
		}

		if err := m.install(ctx, spec); err != nil {
			m.log.Error("install dependency", "component", name, "err", err)
			m.set(name, func(p *Progress) {
				p.Stage = StageFailed
				p.Error = err.Error()
			})
			return
		}
		m.set(name, func(p *Progress) { p.Stage = StageDone })
		m.log.Info("dependency installed", "component", name)

		m.mu.Lock()
		hook := m.installed
		m.mu.Unlock()
		if hook != nil {
			hook(name)
		}
	}()
	return nil
}

func (m *Manager) Status() []Progress {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]Progress, 0, len(m.running))
	for _, p := range m.running {
		out = append(out, *p)
	}
	return out
}

func (m *Manager) set(name string, apply func(*Progress)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p, ok := m.running[name]; ok {
		apply(p)
	}
}

func (m *Manager) install(ctx context.Context, spec *component) error {
	release, err := spec.resolve(ctx, m)
	if err != nil {
		return fmt.Errorf("resolve: %w", err)
	}
	m.set(spec.name, func(p *Progress) {
		p.Stage = StageDownloading
		p.Version = release.Version
	})

	if err := os.MkdirAll(m.binDir, 0o755); err != nil {
		return err
	}
	staging, err := os.MkdirTemp("", "kuro-deps-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)

	name := filepath.Base(release.URL)
	if !release.Archive {
		name = spec.files[0]
	}
	archive := filepath.Join(staging, name)
	if err := m.download(ctx, spec.name, release.URL, archive, release.Digest); err != nil {
		return fmt.Errorf("download: %w", err)
	}

	m.set(spec.name, func(p *Progress) { p.Stage = StageExtracting })

	if !release.Archive {
		if err := install(archive, filepath.Join(m.binDir, spec.files[0])); err != nil {
			return err
		}
		return m.record(spec.name, release.Version)
	}

	unpacked := filepath.Join(staging, "unpacked")
	if err := extract(ctx, archive, unpacked); err != nil {
		return fmt.Errorf("extract: %w", err)
	}

	if spec.glob != "" {
		into := filepath.Join(m.binDir, spec.into)
		if err := os.MkdirAll(into, 0o755); err != nil {
			return err
		}
		found, err := findAll(unpacked, spec.glob)
		if err != nil || len(found) == 0 {
			return fmt.Errorf("no %s in the archive", spec.glob)
		}
		for _, src := range found {
			if err := install(src, filepath.Join(into, filepath.Base(src))); err != nil {
				return err
			}
		}
		return m.record(spec.name, release.Version)
	}

	for _, want := range spec.files {
		src, err := findOne(unpacked, want)
		if err != nil {
			return err
		}
		if err := install(src, filepath.Join(m.binDir, want)); err != nil {
			return err
		}
	}
	return m.record(spec.name, release.Version)
}

func (m *Manager) download(ctx context.Context, name, url, dest, digest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)

	res, err := m.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: HTTP %d", url, res.StatusCode)
	}

	m.set(name, func(p *Progress) { p.Total = res.ContentLength })

	f, err := os.Create(dest)
	if err != nil {
		return err
	}

	started := time.Now()
	sum := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, sum), &counter{
		reader: res.Body,
		report: func(n int64) { m.set(name, func(p *Progress) { p.Bytes = n }) },
	})
	// Closed before the verdict: Windows will not remove a file still open.
	f.Close()
	if err != nil {
		return err
	}

	if digest != "" && !strings.EqualFold(hex.EncodeToString(sum.Sum(nil)), digest) {
		os.Remove(dest)
		return fmt.Errorf("%s: checksum mismatch, refusing to install", name)
	}

	// ffmpeg is 420 MB and people report it as "slow" without a number. Said
	// plainly here it separates a slow line from a slow disk from kuro.
	took := time.Since(started)
	m.log.Info("downloaded", "component", name,
		"mb", n>>20, "seconds", took.Round(time.Second).Seconds(),
		"mbps", fmt.Sprintf("%.1f", float64(n)/took.Seconds()/(1<<20)))
	return nil
}

// Reports every few megabytes; per read would spend more time locking than
// copying.
type counter struct {
	reader   io.Reader
	report   func(int64)
	total    int64
	reported int64
}

func (c *counter) Read(p []byte) (int, error) {
	n, err := c.reader.Read(p)
	c.total += int64(n)
	if c.total-c.reported > 4<<20 {
		c.reported = c.total
		c.report(c.total)
	}
	if err == io.EOF {
		c.report(c.total)
	}
	return n, err
}

// Windows ships bsdtar, which reads 7-Zip: needing a separate 7-Zip install to
// unpack ffmpeg would defeat the point of fetching it here.
func extract(ctx context.Context, archive, dest string) error {
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	if strings.EqualFold(filepath.Ext(archive), ".zip") {
		return unzip(archive, dest)
	}

	// A colon means host:path to tar, so an absolute Windows path reads as a
	// machine called C. It runs from the directory instead, on a bare name.
	name := filepath.Base(archive)
	local := filepath.Join(dest, name)
	if err := os.Rename(archive, local); err != nil {
		return err
	}
	defer os.Remove(local)

	cmd := exec.CommandContext(ctx, systemTar(), "-xf", name)
	cmd.Dir = dest
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tar: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// systemTar names Windows' own bsdtar rather than trusting PATH: Git/MSYS put
// GNU tar first, and GNU tar cannot read the 7-Zip that ffmpeg and mpv ship in.
func systemTar() string {
	if runtime.GOOS != "windows" {
		return "tar"
	}
	if root := os.Getenv("SystemRoot"); root != "" {
		if path := filepath.Join(root, "System32", "tar.exe"); exists(path) {
			return path
		}
	}
	return "tar.exe"
}

func exists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func unzip(archive, dest string) error {
	r, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		// A crafted archive can name ../ paths.
		target := filepath.Join(dest, filepath.FromSlash(f.Name))
		if !strings.HasPrefix(target, filepath.Clean(dest)+string(os.PathSeparator)) {
			return fmt.Errorf("archive entry escapes the target directory: %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := writeEntry(f, target); err != nil {
			return err
		}
	}
	return nil
}

func writeEntry(f *zip.File, target string) error {
	src, err := f.Open()
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	defer dst.Close()

	_, err = io.Copy(dst, src)
	return err
}

// install replaces the target, which fails while the program is running — kuro
// holds ffmpeg open for as long as something is playing.
func install(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return fmt.Errorf("write %s (is it still running?): %w", filepath.Base(dest), err)
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

func findOne(root, name string) (string, error) {
	found, err := findAll(root, name)
	if err != nil {
		return "", err
	}
	if len(found) == 0 {
		return "", fmt.Errorf("%s not found in the archive", name)
	}
	return found[0], nil
}

func findAll(root, pattern string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if ok, _ := filepath.Match(pattern, d.Name()); ok {
			out = append(out, path)
		}
		return nil
	})
	return out, err
}

// record mirrors what fetch-deps.ps1 writes, so the two are interchangeable.
func (m *Manager) record(name, version string) error {
	path := filepath.Join(m.binDir, "versions.json")

	versions := map[string]string{}
	if raw, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(raw, &versions)
	}
	versions[name] = version

	raw, err := json.MarshalIndent(versions, "", "    ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o644)
}

var semverTag = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)
