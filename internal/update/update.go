// Package update replaces kuro.exe with the newest GitHub release. The zip is
// the only thing touched: bin/, cache/, config.toml and the database stay.
package update

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"kuro/internal/config"
	"kuro/internal/store"
)

// Version is stamped by the build (-X kuro/internal/update.Version=...); a
// "dev" build never updates itself.
var Version = "dev"

const (
	Repo      = "Tons-7/kuro"
	userAgent = "kuro"
	sumsFile  = "SHA256SUMS.txt"
)

type Stage string

const (
	StageIdle        Stage = "idle"
	StageDownloading Stage = "downloading"
	StageVerifying   Stage = "verifying"
	StageRestarting  Stage = "restarting"
	StageFailed      Stage = "failed"
)

type Release struct {
	Version string `json:"version"`
	URL     string `json:"url"`
	Sums    string `json:"-"`
	Notes   string `json:"notes,omitempty"`
}

type Status struct {
	Current   string    `json:"current"`
	Latest    *Release  `json:"latest,omitempty"`
	Available bool      `json:"available"`
	CheckedAt time.Time `json:"checkedAt,omitzero"`
	CheckErr  string    `json:"checkError,omitempty"`
	Stage     Stage     `json:"stage"`
	Bytes     int64     `json:"bytes"`
	Total     int64     `json:"total"`
	Error     string    `json:"error,omitempty"`
}

type Updater struct {
	store   *store.Store
	exe     string
	staging string
	api     string
	http    *http.Client
	log     *slog.Logger
	restart func()
	launch  func(exe string) error

	mu     sync.Mutex
	status Status
}

func New(st *store.Store, exe, staging string, log *slog.Logger) *Updater {
	api := "https://api.github.com"
	// An end-to-end test points a real build at a fake release server.
	if override := os.Getenv("KURO_UPDATE_API"); override != "" {
		api = override
	}
	return &Updater{
		store: st, exe: exe, staging: staging,
		api:    api,
		http:   &http.Client{Timeout: 10 * time.Minute},
		log:    log,
		launch: relaunch,
		status: Status{Current: Version, Stage: StageIdle},
	}
}

// WithRestart sets what ends this process once the new binary is in place.
func (u *Updater) WithRestart(f func()) *Updater { u.restart = f; return u }

func (u *Updater) Status() Status {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.status
}

func (u *Updater) set(apply func(*Status)) {
	u.mu.Lock()
	apply(&u.status)
	u.mu.Unlock()
}

type ghRelease struct {
	Tag    string `json:"tag_name"`
	Body   string `json:"body"`
	Assets []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

// Check asks GitHub for the latest release and raises a notification the first
// time a newer one is seen. A dev build only records what is out.
func (u *Updater) Check(ctx context.Context) (Status, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	rel, err := u.latest(ctx)
	u.set(func(s *Status) {
		s.CheckedAt = time.Now()
		s.CheckErr = ""
		if err != nil {
			s.CheckErr = err.Error()
			return
		}
		s.Latest = &rel
		s.Available = rel.URL != "" && Newer(rel.Version, Version)
	})
	if err != nil {
		return u.Status(), err
	}

	if st := u.Status(); st.Available {
		u.notify(ctx, rel)
	}
	return u.Status(), nil
}

func (u *Updater) latest(ctx context.Context) (Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		u.api+"/repos/"+Repo+"/releases/latest", nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/vnd.github+json")

	res, err := u.http.Do(req)
	if err != nil {
		return Release{}, err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return Release{}, errors.New("no releases published yet")
	}
	if res.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("github: HTTP %d", res.StatusCode)
	}

	var gh ghRelease
	if err := json.NewDecoder(res.Body).Decode(&gh); err != nil {
		return Release{}, err
	}

	rel := Release{Version: strings.TrimPrefix(gh.Tag, "v"), Notes: gh.Body}
	want := AssetName(rel.Version)
	for _, a := range gh.Assets {
		switch a.Name {
		case want:
			rel.URL = a.URL
		case sumsFile:
			rel.Sums = a.URL
		}
	}
	return rel, nil
}

// AssetName is the zip package.ps1 publishes for this platform.
func AssetName(version string) string {
	if runtime.GOOS == "windows" {
		return fmt.Sprintf("kuro-%s-update.zip", version)
	}
	return fmt.Sprintf("kuro-%s-%s-%s.zip", version, runtime.GOOS, runtime.GOARCH)
}

// notify announces a version once; the setting remembers which.
func (u *Updater) notify(ctx context.Context, rel Release) {
	if u.store == nil {
		return
	}
	if seen, _ := u.store.Setting(ctx, "update.notified"); seen == rel.Version {
		return
	}
	if _, err := u.store.AddNotification(ctx, store.Notification{
		Kind:    store.NotifyUpdate,
		Title:   fmt.Sprintf("kuro %s is available", rel.Version),
		Body:    "Open Settings → About to update",
		Payload: map[string]any{"version": rel.Version},
	}); err != nil {
		u.log.Warn("update notification", "err", err)
		return
	}
	if err := u.store.SetSetting(ctx, "update.notified", rel.Version); err != nil {
		u.log.Warn("remember update notification", "err", err)
	}
}

// Apply fetches the latest release in the background, swaps the executable and
// starts the new one; the caller's restart hook then ends this process.
func (u *Updater) Apply() error {
	st := u.Status()
	if st.Stage == StageDownloading || st.Stage == StageVerifying || st.Stage == StageRestarting {
		return nil
	}
	if !st.Available || st.Latest == nil {
		return errors.New("no update available")
	}
	rel := *st.Latest
	u.set(func(s *Status) { s.Stage, s.Bytes, s.Total, s.Error = StageDownloading, 0, 0, "" })

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()

		if err := u.apply(ctx, rel); err != nil {
			u.log.Error("update", "version", rel.Version, "err", err)
			u.set(func(s *Status) { s.Stage, s.Error = StageFailed, err.Error() })
			return
		}
		u.set(func(s *Status) { s.Stage = StageRestarting })
		u.log.Info("restarting into the new version", "version", rel.Version)
		if u.restart != nil {
			u.restart()
		}
	}()
	return nil
}

func (u *Updater) apply(ctx context.Context, rel Release) error {
	if rel.Sums == "" {
		return errors.New("release carries no " + sumsFile + "; refusing an unverified binary")
	}
	if err := os.MkdirAll(u.staging, 0o755); err != nil {
		return err
	}
	archive := filepath.Join(u.staging, filepath.Base(rel.URL))
	defer os.Remove(archive)

	if err := u.download(ctx, rel.URL, archive); err != nil {
		return fmt.Errorf("download: %w", err)
	}

	u.set(func(s *Status) { s.Stage = StageVerifying })
	if err := u.verify(ctx, rel, archive); err != nil {
		return err
	}

	fresh := u.exe + ".new"
	if err := extractExe(archive, fresh); err != nil {
		return fmt.Errorf("unpack: %w", err)
	}
	if err := swap(u.exe, fresh); err != nil {
		os.Remove(fresh)
		return fmt.Errorf("replace executable: %w", err)
	}
	if err := u.launch(u.exe); err != nil {
		return fmt.Errorf("start the new version: %w", err)
	}
	return nil
}

func (u *Updater) download(ctx context.Context, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)

	res, err := u.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", res.StatusCode)
	}
	u.set(func(s *Status) { s.Total = res.ContentLength })

	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()

	var done int64
	buf := make([]byte, 256<<10)
	for {
		n, rerr := res.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				return werr
			}
			done += int64(n)
			u.set(func(s *Status) { s.Bytes = done })
		}
		if rerr == io.EOF {
			return nil
		}
		if rerr != nil {
			return rerr
		}
	}
}

// verify checks the zip against the published SHA256SUMS line for it.
func (u *Updater) verify(ctx context.Context, rel Release, archive string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rel.Sums, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	res, err := u.http.Do(req)
	if err != nil {
		return fmt.Errorf("checksums: %w", err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	if err != nil {
		return err
	}

	want := ""
	name := filepath.Base(archive)
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name {
			want = strings.ToLower(fields[0])
		}
	}
	if want == "" {
		return fmt.Errorf("%s has no entry for %s", sumsFile, name)
	}

	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return fmt.Errorf("checksum mismatch: got %s, want %s", got, want)
	}
	return nil
}

// extractExe writes the one executable in the zip to dest.
func extractExe(archive, dest string) error {
	r, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer r.Close()

	want := config.ExeName("kuro")
	for _, f := range r.File {
		if filepath.Base(f.Name) != want || f.FileInfo().IsDir() {
			continue
		}
		src, err := f.Open()
		if err != nil {
			return err
		}
		defer src.Close()

		out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, src)
		return err
	}
	return fmt.Errorf("%s not in the archive", want)
}

// swap moves the running binary aside (allowed on every OS) and puts the new
// one in its place; a failure puts the old one back.
func swap(exe, fresh string) error {
	old := exe + ".old"
	os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		return err
	}
	if err := os.Rename(fresh, exe); err != nil {
		os.Rename(old, exe)
		return err
	}
	return nil
}

// Cleanup removes what the last update left behind. Called at startup, when
// the old binary has exited and can be deleted.
func Cleanup(exe string) {
	os.Remove(exe + ".old")
	os.Remove(exe + ".new")
}

// relaunch starts the new binary outside this process's job object, told to
// wait for this one to exit before it binds the port. The app window dies with
// this process, so the window choice carries over instead of being suppressed —
// a windowed kuro reopens its UI.
func relaunch(exe string) error { return relaunchCmd(exe).Start() }

func relaunchCmd(exe string) *exec.Cmd {
	cmd := exec.Command(exe, relaunchArgs(os.Getpid(), os.Args[1:])...)
	cmd.Dir = filepath.Dir(exe)
	// Inherited, not left at the default of NUL: a handover that fails at
	// startup would otherwise take the app away without printing why.
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	config.Detach(cmd)
	return cmd
}

// relaunchArgs: a fresh --wait-for, the old pair dropped, the rest carried over.
func relaunchArgs(pid int, current []string) []string {
	args := []string{"--wait-for", strconv.Itoa(pid)}
	skip := false
	for _, a := range current {
		switch {
		case skip:
			skip = false
		case a == "--wait-for":
			skip = true
		default:
			args = append(args, a)
		}
	}
	return args
}

// WaitFor reads --wait-for <pid> from the arguments and blocks until that
// process is gone, so a relaunched kuro does not race its predecessor for the port.
func WaitFor(args []string, timeout time.Duration) {
	for i, a := range args {
		if a != "--wait-for" || i+1 >= len(args) {
			continue
		}
		pid, err := strconv.Atoi(args[i+1])
		if err != nil || pid <= 0 {
			return
		}
		deadline := time.Now().Add(timeout)
		for alive(pid) && time.Now().Before(deadline) {
			time.Sleep(200 * time.Millisecond)
		}
		return
	}
}

// Newer reports whether a is a later version than b: dotted numbers compared
// left to right, so 2026.08.27 < 2026.08.27.1 and a "dev" build is never behind.
func Newer(a, b string) bool {
	if b == "dev" || a == "" {
		return false
	}
	as, bs := parts(a), parts(b)
	for i := 0; i < len(as) || i < len(bs); i++ {
		var x, y int
		if i < len(as) {
			x = as[i]
		}
		if i < len(bs) {
			y = bs[i]
		}
		if x != y {
			return x > y
		}
	}
	return false
}

func parts(v string) []int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	var out []int
	for _, p := range strings.FieldsFunc(v, func(r rune) bool { return r == '.' || r == '-' }) {
		n, err := strconv.Atoi(p)
		if err != nil {
			break
		}
		out = append(out, n)
	}
	return out
}
