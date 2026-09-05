package update

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"kuro/internal/config"
	"kuro/internal/db"
	"kuro/internal/store"
)

// --no-window is forwarded, never added, so a windowed kuro reopens its UI.
func TestRelaunchForwardsTheWindowChoice(t *testing.T) {
	for _, tc := range []struct {
		current []string
		want    []string
	}{
		{nil, []string{"--wait-for", "42"}},
		{[]string{"--no-window", "--other"}, []string{"--wait-for", "42", "--no-window", "--other"}},
		{[]string{"--wait-for", "7", "--no-window"}, []string{"--wait-for", "42", "--no-window"}},
	} {
		got := relaunchArgs(42, tc.current)
		if fmt.Sprint(got) != fmt.Sprint(tc.want) {
			t.Errorf("relaunchArgs(42, %v) = %v, want %v", tc.current, got, tc.want)
		}
	}
}

// With the child's output on NUL, a handover that failed took the app away
// without a word.
func TestRelaunchKeepsTheOutputItWasStartedWith(t *testing.T) {
	cmd := relaunchCmd(filepath.Join(t.TempDir(), config.ExeName("kuro")))

	if cmd.Stdout != os.Stdout {
		t.Errorf("stdout = %v, want the parent's", cmd.Stdout)
	}
	if cmd.Stderr != os.Stderr {
		t.Errorf("stderr = %v, want the parent's", cmd.Stderr)
	}
	if len(cmd.Args) < 3 || cmd.Args[1] != "--wait-for" {
		t.Errorf("args = %v, want it to wait for this process", cmd.Args)
	}
	if cmd.Dir != filepath.Dir(cmd.Path) {
		t.Errorf("dir = %q, want the executable's own directory", cmd.Dir)
	}
}

func TestNewer(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"2026.08.27", "2026.08.24", true},
		{"v2026.08.27", "2026.08.27", false},
		{"2026.08.27.1", "2026.08.27", true},
		{"2026.09.01", "2026.08.27.3", true},
		{"2026.08.27", "dev", false},
		{"", "2026.08.27", false},
	} {
		if got := Newer(tc.a, tc.b); got != tc.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func testStore(t *testing.T) *store.Store {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := conn.Migrate(); err != nil {
		t.Fatal(err)
	}
	return store.New(conn)
}

// fakeGitHub serves one release whose update zip holds a stand-in executable.
func fakeGitHub(t *testing.T, version string, exeBody []byte, sums bool) *httptest.Server {
	t.Helper()

	var archive bytes.Buffer
	zw := zip.NewWriter(&archive)
	f, _ := zw.Create(config.ExeName("kuro"))
	f.Write(exeBody)
	zw.Close()
	sum := sha256.Sum256(archive.Bytes())

	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/repos/"+Repo+"/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		assets := []map[string]string{
			{"name": AssetName(version), "browser_download_url": srv.URL + "/dl/" + AssetName(version)},
		}
		if sums {
			assets = append(assets, map[string]string{"name": sumsFile, "browser_download_url": srv.URL + "/dl/" + sumsFile})
		}
		json.NewEncoder(w).Encode(map[string]any{"tag_name": "v" + version, "body": "notes", "assets": assets})
	})
	mux.HandleFunc("/dl/"+AssetName(version), func(w http.ResponseWriter, r *http.Request) {
		w.Write(archive.Bytes())
	})
	mux.HandleFunc("/dl/"+sumsFile, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(sum[:]), AssetName(version))
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func setVersion(t *testing.T, v string) {
	t.Helper()
	was := Version
	Version = v
	t.Cleanup(func() { Version = was })
}

func newUpdater(t *testing.T, st *store.Store, gh *httptest.Server, exe string) *Updater {
	t.Helper()
	u := New(st, exe, filepath.Join(t.TempDir(), "staging"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	u.api = gh.URL
	return u
}

func TestCheckNotifiesOncePerVersion(t *testing.T) {
	setVersion(t, "2026.08.24")
	st := testStore(t)
	gh := fakeGitHub(t, "2026.08.27", []byte("new"), true)
	u := newUpdater(t, st, gh, filepath.Join(t.TempDir(), "kuro.exe"))
	ctx := context.Background()

	for range 2 {
		got, err := u.Check(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !got.Available || got.Latest == nil || got.Latest.Version != "2026.08.27" {
			t.Fatalf("status = %+v, want 2026.08.27 available", got)
		}
	}

	items, _ := st.Notifications(ctx, false, 10)
	if len(items) != 1 || items[0].Kind != store.NotifyUpdate {
		t.Fatalf("notifications = %+v, want exactly one update notice", items)
	}
}

func TestDevBuildNeverSeesAnUpdate(t *testing.T) {
	setVersion(t, "dev")
	st := testStore(t)
	gh := fakeGitHub(t, "2026.08.27", []byte("new"), true)
	u := newUpdater(t, st, gh, filepath.Join(t.TempDir(), "kuro.exe"))

	got, err := u.Check(context.Background())
	if err != nil || got.Available {
		t.Fatalf("status = %+v err = %v, want nothing offered to a dev build", got, err)
	}
	if err := u.Apply(); err == nil {
		t.Fatal("apply must refuse when nothing is available")
	}
}

func TestApplySwapsTheExecutableAndHandsOver(t *testing.T) {
	setVersion(t, "2026.08.24")
	st := testStore(t)
	gh := fakeGitHub(t, "2026.08.27", []byte("new binary"), true)

	exe := filepath.Join(t.TempDir(), config.ExeName("kuro"))
	os.WriteFile(exe, []byte("old binary"), 0o755)

	var launched string
	restarted := make(chan struct{})
	u := newUpdater(t, st, gh, exe).WithRestart(func() { close(restarted) })
	u.launch = func(path string) error { launched = path; return nil }

	if _, err := u.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := u.Apply(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-restarted:
	case <-time.After(10 * time.Second):
		t.Fatalf("no restart; status = %+v", u.Status())
	}

	if got, _ := os.ReadFile(exe); string(got) != "new binary" {
		t.Errorf("executable = %q, want the new one in place", got)
	}
	if got, _ := os.ReadFile(exe + ".old"); string(got) != "old binary" {
		t.Errorf("old binary = %q, want it kept aside until the next start", got)
	}
	if launched != exe {
		t.Errorf("launched %q, want the replaced executable", launched)
	}
	if st := u.Status(); st.Stage != StageRestarting || st.Bytes == 0 {
		t.Errorf("status = %+v", st)
	}

	Cleanup(exe)
	if _, err := os.Stat(exe + ".old"); err == nil {
		t.Error("cleanup left the old binary behind")
	}
}

func TestApplyRefusesAnUnverifiableArchive(t *testing.T) {
	setVersion(t, "2026.08.24")
	st := testStore(t)
	gh := fakeGitHub(t, "2026.08.27", []byte("new"), false)

	exe := filepath.Join(t.TempDir(), config.ExeName("kuro"))
	os.WriteFile(exe, []byte("old"), 0o755)
	u := newUpdater(t, st, gh, exe).WithRestart(func() { t.Error("restarted on an unverified update") })
	u.launch = func(string) error { t.Error("launched an unverified update"); return nil }

	u.Check(context.Background())
	if err := u.Apply(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for u.Status().Stage != StageFailed && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if st := u.Status(); st.Stage != StageFailed || st.Error == "" {
		t.Fatalf("status = %+v, want a failure naming the missing checksums", st)
	}
	if got, _ := os.ReadFile(exe); string(got) != "old" {
		t.Error("the executable was replaced without verification")
	}
}

func TestSwapRestoresTheOldBinaryWhenTheNewOneCannotMoveIn(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "kuro.exe")
	os.WriteFile(exe, []byte("old"), 0o755)

	if err := swap(exe, filepath.Join(dir, "missing.new")); err == nil {
		t.Fatal("swap succeeded without a new binary")
	}
	if got, _ := os.ReadFile(exe); string(got) != "old" {
		t.Errorf("executable = %q after a failed swap", got)
	}
}
