package config

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Addr     string    `toml:"addr"`
	CacheDir string    `toml:"cache_dir"`
	BinDir   string    `toml:"bin_dir"`
	AniList  AniList   `toml:"anilist"`
	MAL      MAL       `toml:"mal"`
	Torrent  Torrent   `toml:"torrent"`
	Indexers []Indexer `toml:"indexer"`
	// Data is where the database and window profile go; empty means AppData.
	// Relative to the exe folder.
	Data string `toml:"data_dir"`
	// VLC is the player's binary or install folder; empty means the usual places.
	VLC string `toml:"vlc_path"`

	dataDir   string
	root      string
	temporary bool
}

func (c Config) ConfigPath() string { return filepath.Join(c.root, "config.toml") }

// StrayConfig is a "config.toml.txt" beside the real file: what Notepad's
// Save As produces, and what kuro will never read.
func (c Config) StrayConfig() string {
	p := c.ConfigPath() + ".txt"
	if exists(p) {
		return p
	}
	return ""
}

// Temporary reports a root inside the OS temp directory: an exe run from
// inside a zip lands there, and nothing written beside it survives.
func (c Config) Temporary() bool { return c.temporary }

// Indexer is one torrent search site. None ship with kuro.
type Indexer struct {
	// Type is the feed format: "nyaa" or "tokyotosho".
	Type string `toml:"type"`
	URL  string `toml:"url"`
	// Adult sources are searched only for titles the catalogue marks adult.
	Adult bool `toml:"adult"`
}

// Torrent tunes the rqbit engine. The defaults trade a little upload and an
// open port for download speed, which is what most home connections want; a
// user on a metered or locked-down network can dial them back here.
// DefaultTorrentAPIAddr is where kuro's own rqbit listens.
const DefaultTorrentAPIAddr = "127.0.0.1:3030"

type Torrent struct {
	// APIAddr is rqbit's API. Loopback (default) is kuro's own; another host
	// is an engine elsewhere, used as found and never spawned.
	APIAddr string `toml:"api_addr"`
	// UploadLimitBytes caps upload in bytes/sec. Public trackers give nothing
	// back for seeding, but some upload feeds the tit-for-tat that keeps peers
	// unchoking us, so a middling cap downloads faster than a tiny one.
	UploadLimitBytes int `toml:"upload_limit_bytes"`
	// ListenPort is the peer port; 0 leaves rqbit's default (4240). Forwarding
	// it — see UPnP — is what lets peers connect in, which is most of the speed.
	ListenPort int `toml:"listen_port"`
	// PeerLimit is the max peers per torrent; 0 leaves rqbit's default.
	PeerLimit int `toml:"peer_limit"`
	// UPnP asks the router to forward ListenPort so peers can reach us. Nil
	// defaults to on; behind a NAT without it the swarm is only the peers that
	// happen to be reachable outbound, which is the usual cause of slow grabs.
	UPnP *bool `toml:"upnp"`
}

// UPnPEnabled reports the port-forward setting, on unless explicitly disabled.
func (t Torrent) UPnPEnabled() bool { return t.UPnP == nil || *t.UPnP }

type AniList struct {
	ClientID     string `toml:"client_id"`
	ClientSecret string `toml:"client_secret"`
}

type MAL struct {
	ClientID     string `toml:"client_id"`
	ClientSecret string `toml:"client_secret"`
}

// LocalURL is where the interface lives for this machine. A wildcard bind is
// reachable at every address, but only loopback is meaningful to open here.
func (c Config) LocalURL() string {
	_, port, err := net.SplitHostPort(c.Addr)
	if err != nil {
		port = "4321"
	}
	return "http://localhost:" + port
}

func appProfileDir() string { return filepath.Join(dataDir(), "window") }

func exists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// ResolveDataDir is where a data_dir setting points: AppData when empty,
// otherwise the path, relative to the exe folder.
func (c Config) ResolveDataDir(dir string) string { return c.resolve(dir, dataDir()) }

// resolve reads a folder setting; relative paths are beside the exe.
func (c Config) resolve(dir, fallback string) string {
	switch {
	case dir == "":
		return fallback
	case filepath.IsAbs(dir):
		return filepath.Clean(dir)
	}
	return filepath.Join(c.root, dir)
}

// SetDataDir writes data_dir into config.toml, replacing the line if there is
// one and otherwise placing it before the first table, where a top-level key
// has to be. Takes effect on the next start.
func (c Config) SetDataDir(dir string) error {
	raw, err := os.ReadFile(c.ConfigPath())
	if err != nil {
		return err
	}
	line := fmt.Sprintf("data_dir = %q", filepath.ToSlash(dir))
	lines := strings.Split(string(raw), "\n")
	placed := false
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "data_dir") {
			lines[i], placed = line, true
			break
		}
	}
	if !placed {
		at := len(lines)
		for i, l := range lines {
			if strings.HasPrefix(strings.TrimSpace(l), "[") {
				at = i
				break
			}
		}
		lines = append(lines[:at], append([]string{line, ""}, lines[at:]...)...)
	}
	return os.WriteFile(c.ConfigPath(), []byte(strings.Join(lines, "\n")), 0o600)
}

// VLCPath is the vlc_path setting, empty when unset.
func (c Config) VLCPath() string {
	if c.VLC == "" {
		return ""
	}
	return c.resolve(c.VLC, "")
}

func (c Config) DataDir() string      { return c.dataDir }
func (c Config) DatabasePath() string { return filepath.Join(c.dataDir, "kuro.db") }
func (c Config) ProfileDir() string   { return filepath.Join(c.dataDir, "window") }
func (c Config) Path(name string) string {
	return filepath.Join(c.BinDir, name)
}

// ExeName is the platform's filename for an executable base name: kuro fetches
// "ffmpeg" as ffmpeg.exe on Windows, ffmpeg elsewhere.
func ExeName(base string) string {
	if runtime.GOOS == "windows" {
		return base + ".exe"
	}
	return base
}

// Tool resolves an engine binary by base name: the one kuro downloaded into
// bin/ if it is there, otherwise one the OS provides on PATH (a brew/apt/pacman
// install), falling back to the bin/ path so an error names where it looked.
func (c Config) Tool(base string) string {
	p := c.Path(ExeName(base))
	if exists(p) {
		return p
	}
	if onPath, err := exec.LookPath(ExeName(base)); err == nil {
		return onPath
	}
	return p
}

// Must match the redirect URL registered on AniList exactly, including port.
// Served by the main listener so there is no second server to coordinate.
func (c Config) RedirectURI() string { return c.redirect("/callback") }

// MyAnimeList registers its own redirect URL, on a separate path so one
// callback handler does not have to guess which provider answered.
func (c Config) MALRedirectURI() string { return c.redirect("/mal/callback") }

func (c Config) redirect(path string) string {
	_, port, err := net.SplitHostPort(c.Addr)
	if err != nil {
		port = "4321"
	}
	return "http://localhost:" + port + path
}

// Load reads config.toml from the executable's directory, writing a template on
// first run. Secrets live here rather than in the database so the file can stay
// out of version control.
func Load() (Config, error) {
	root, err := rootDir()
	if err != nil {
		return Config{}, err
	}

	cfg := Config{Addr: "127.0.0.1:4321", dataDir: dataDir()}
	// The engine defaults this too, but on its own copy; anything reading the
	// address from the config would otherwise see an empty string.
	cfg.Torrent.APIAddr = DefaultTorrentAPIAddr

	path := filepath.Join(root, "config.toml")
	switch _, err := toml.DecodeFile(path, &cfg); {
	case errors.Is(err, fs.ErrNotExist):
		if err := os.WriteFile(path, []byte(template), 0o600); err != nil {
			return Config{}, err
		}
	case err != nil:
		return Config{}, err
	}

	cfg.root, cfg.temporary = root, underTemp(root)
	cfg.dataDir = cfg.ResolveDataDir(cfg.Data)
	cfg.CacheDir = cfg.resolve(cfg.CacheDir, filepath.Join(root, "cache"))
	cfg.BinDir = cfg.resolve(cfg.BinDir, filepath.Join(root, "bin"))
	for _, dir := range []string{cfg.dataDir, cfg.CacheDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return Config{}, err
		}
	}
	return cfg, nil
}

// Root is where config.toml, bin/ and cache/ live: normally beside the
// executable, overridable with KURO_ROOT.
func rootDir() (string, error) {
	if root := os.Getenv("KURO_ROOT"); root != "" {
		return root, nil
	}

	exe, err := os.Executable()
	if err != nil {
		return os.Getwd()
	}
	dir := filepath.Dir(exe)

	// `go run` and `go test` build into the system temp directory, where a
	// config file would vanish. Fall back to the working tree instead.
	if underTemp(dir) {
		return os.Getwd()
	}
	return dir, nil
}

func underTemp(dir string) bool {
	tmp, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		return false
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(tmp, resolved)
	return err == nil && !strings.HasPrefix(rel, "..")
}

// os.UserConfigDir resolves to Roaming on Windows, where a live WAL database
// would be synced across machines. Local is the correct home for it.
func dataDir() string {
	if d := os.Getenv("LOCALAPPDATA"); d != "" {
		return filepath.Join(d, "kuro")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".kuro")
}

const template = `# Register an application at https://anilist.co/settings/developer
# and set its redirect URL to exactly: http://localhost:4321/callback
# Then visit http://localhost:4321/api/auth/login to connect your account.

# Use 0.0.0.0:4321 to reach kuro from a phone or TV on the same network.
# Off the host machine a token is required; GET /api/access prints the URL and
# /api/access/qr.svg renders it as a QR code.
addr = "127.0.0.1:4321"

[anilist]
client_id = ""
client_secret = ""

# Optional second tracker. Register at https://myanimelist.net/apiconfig
# with redirect URL exactly: http://localhost:4321/mal/callback
[mal]
client_id = ""
client_secret = ""

# Folders. Relative paths are beside kuro.exe; quote Windows paths with single
# quotes ('D:\kuro\cache'). Restart after editing.
# data_dir: database and window profile (default %LOCALAPPDATA%\kuro)
# cache_dir: downloaded episodes, transcodes, thumbnails, updates
# bin_dir: rqbit, ffmpeg, mpv, shaders
# data_dir = ""
# cache_dir = "cache"
# bin_dir = "bin"

# VLC, when it is not on PATH or in Program Files: its folder or its binary.
# vlc_path = 'E:\VideoLAN\VLC'

# Torrent search sites, one block each. kuro ships with none. type is the feed
# format ("nyaa" or "tokyotosho"); adult = true marks a site searched only for
# adult titles. The first listed wins ties. Restart after editing.
#
# [[indexer]]
# type = "nyaa"
# url = ""
`
