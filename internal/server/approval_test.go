package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kuro/internal/certs"
	"kuro/internal/config"
)

const phone = "192.168.1.50:5555"

// device is a remote browser: it keeps cookies between requests like one.
type device struct {
	h       *harness
	t       *testing.T
	cookies map[string]string
	ua      string
}

func (d *device) get(target string, page bool) *http.Response {
	d.t.Helper()
	req := httptest.NewRequest("GET", target, nil)
	req.Host, req.RemoteAddr = "192.168.1.10:4321", phone
	req.Header.Set("User-Agent", d.ua)
	if page {
		req.Header.Set("Accept", "text/html,application/xhtml+xml")
	}
	for k, v := range d.cookies {
		req.AddCookie(&http.Cookie{Name: k, Value: v})
	}
	rec := httptest.NewRecorder()
	d.h.handler.ServeHTTP(rec, req)
	res := rec.Result()
	for _, c := range res.Cookies() {
		d.cookies[c.Name] = c.Value
	}
	return res
}

func newDevice(t *testing.T, h *harness) *device {
	return &device{h: h, t: t, cookies: map[string]string{},
		ua: "Mozilla/5.0 (Linux; Android 14) AppleWebKit/537.36 Chrome/126.0 Mobile Safari/537.36"}
}

// host calls a host-only route from the machine itself.
func (h *harness) host(t *testing.T, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Host, req.RemoteAddr = "127.0.0.1:4321", "127.0.0.1:5555"
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)
	return rec
}

func (h *harness) devices(t *testing.T) []map[string]any {
	t.Helper()
	var out struct {
		Devices []map[string]any `json:"devices"`
	}
	if err := json.Unmarshal(h.host(t, "GET", "/api/access/devices", "").Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Devices
}

func approvalHarness(t *testing.T, mode string) *harness {
	h := newHarness(t, config.Config{}, nil)
	h.server.token = "s3cret"
	if rec := h.host(t, "POST", "/api/access/approval", `{"mode":"`+mode+`"}`); rec.Code != http.StatusOK {
		t.Fatalf("set mode: %d %s", rec.Code, rec.Body)
	}
	return h
}

func TestApprovalOffLetsATokenHolderIn(t *testing.T) {
	h := newHarness(t, config.Config{}, nil)
	h.server.token = "s3cret"
	if res := newDevice(t, h).get("/api/health?token=s3cret", false); res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
}

func TestDeviceWaitsUntilTheHostAccepts(t *testing.T) {
	h := approvalHarness(t, approvalOnce)
	d := newDevice(t, h)

	res := d.get("/?token=s3cret", true)
	page, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusForbidden || !strings.Contains(string(page), "Waiting for the host") {
		t.Fatalf("first visit: %d %.80s", res.StatusCode, page)
	}
	if res := d.get("/api/health", false); res.StatusCode != http.StatusForbidden {
		t.Fatalf("api before approval: %d, want 403", res.StatusCode)
	}

	list := h.devices(t)
	if len(list) != 1 || list[0]["status"] != "pending" || list[0]["name"] != "Chrome on Android" {
		t.Fatalf("devices = %v", list)
	}
	id := list[0]["id"].(string)
	if rec := h.host(t, "POST", "/api/access/devices/"+id, `{"status":"approved"}`); rec.Code != http.StatusOK {
		t.Fatalf("approve: %d %s", rec.Code, rec.Body)
	}
	if res := d.get("/api/health", false); res.StatusCode != http.StatusOK {
		t.Fatalf("api after approval: %d, want 200", res.StatusCode)
	}

	// Remembered across a restart: the decision is in the database.
	h.server.approvals = approvals{}
	if res := d.get("/api/health", false); res.StatusCode != http.StatusOK {
		t.Fatalf("after restart: %d, want 200", res.StatusCode)
	}

	h.host(t, "DELETE", "/api/access/devices/"+id, "")
	if res := d.get("/api/health", false); res.StatusCode != http.StatusForbidden {
		t.Fatalf("after removal: %d, want 403", res.StatusCode)
	}
}

func TestDeclinedDeviceStaysOutAndDoesNotAskAgain(t *testing.T) {
	h := approvalHarness(t, approvalOnce)
	d := newDevice(t, h)
	d.get("/?token=s3cret", true)
	id := h.devices(t)[0]["id"].(string)
	h.host(t, "POST", "/api/access/devices/"+id, `{"status":"denied"}`)

	res := d.get("/", true)
	page, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusForbidden || !strings.Contains(string(page), "Access declined") {
		t.Fatalf("declined visit: %d %.80s", res.StatusCode, page)
	}
	if list := h.devices(t); len(list) != 1 || list[0]["status"] != "denied" {
		t.Fatalf("devices = %v", list)
	}
}

func TestApprovalNeedsTheTokenFirst(t *testing.T) {
	h := approvalHarness(t, approvalOnce)
	if res := newDevice(t, h).get("/", true); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", res.StatusCode)
	}
	if list := h.devices(t); len(list) != 0 {
		t.Fatalf("a device without the link made a request: %v", list)
	}
}

func TestScriptsWithoutCookiesMakeNoRequests(t *testing.T) {
	h := approvalHarness(t, approvalOnce)
	for range 5 {
		newDevice(t, h).get("/api/health?token=s3cret", false)
	}
	if list := h.devices(t); len(list) != 0 {
		t.Fatalf("devices = %v, want none", list)
	}
}

func TestPendingRequestsAreCapped(t *testing.T) {
	h := approvalHarness(t, approvalOnce)
	for range maxPending + 5 {
		newDevice(t, h).get("/?token=s3cret", true)
	}
	if list := h.devices(t); len(list) != maxPending {
		t.Fatalf("%d pending, want %d", len(list), maxPending)
	}
}

func TestAlwaysModeAsksAgainAfterTheDeviceGoesQuiet(t *testing.T) {
	h := approvalHarness(t, approvalAlways)
	d := newDevice(t, h)
	d.get("/?token=s3cret", true)
	id := h.devices(t)[0]["id"].(string)
	h.host(t, "POST", "/api/access/devices/"+id, `{"status":"approved"}`)
	if res := d.get("/api/health", false); res.StatusCode != http.StatusOK {
		t.Fatalf("approved: %d", res.StatusCode)
	}

	h.server.approvals.mu.Lock()
	h.server.approvals.seen[id] = time.Now().Add(-approvalIdle - time.Minute)
	h.server.approvals.mu.Unlock()

	if res := d.get("/api/health", false); res.StatusCode != http.StatusForbidden {
		t.Fatalf("after going quiet: %d, want 403", res.StatusCode)
	}
	if list := h.devices(t); list[0]["status"] != "pending" {
		t.Fatalf("status = %v, want pending again", list[0]["status"])
	}
}

func TestRemoteDeviceCannotApproveItself(t *testing.T) {
	h := approvalHarness(t, approvalOnce)
	d := newDevice(t, h)
	d.get("/?token=s3cret", true)
	id := h.devices(t)[0]["id"].(string)

	req := httptest.NewRequest("POST", "/api/access/devices/"+id, strings.NewReader(`{"status":"approved"}`))
	req.Host, req.RemoteAddr = "192.168.1.10:4321", phone
	req.Header.Set("Authorization", "Bearer s3cret")
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestInstallAssetsNeedNoToken(t *testing.T) {
	h := approvalHarness(t, approvalOnce)
	res := newDevice(t, h).get("/manifest.webmanifest", false)
	if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden {
		t.Fatalf("manifest refused: %d", res.StatusCode)
	}
}

func TestDeviceNames(t *testing.T) {
	for ua, want := range map[string]string{
		"Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 Version/17.0 Mobile/15E148 Safari/604.1": "Safari on iPhone",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/126.0 Safari/537.36 Edg/126.0":                   "Edge on Windows",
		"Mozilla/5.0 (X11; Linux x86_64; rv:127.0) Gecko/20100101 Firefox/127.0":                                              "Firefox on Linux",
		"curl/8.5": "Unknown device",
	} {
		if got := deviceName(ua); got != want {
			t.Errorf("%q = %q, want %q", ua, got, want)
		}
	}
}

// selfSigned writes a throwaway certificate for name and returns its paths.
func selfSigned(t *testing.T, name string) (certPath, keyPath string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: name},
		DNSNames: []string{name, "*.wild.example"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, _ := x509.MarshalECPrivateKey(key)
	dir := t.TempDir()
	certPath, keyPath = filepath.Join(dir, "kuro.crt"), filepath.Join(dir, "kuro.key")
	os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600)
	os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600)
	return certPath, keyPath
}

func TestOnePortAnswersHTTPAndHTTPS(t *testing.T) {
	certPath, keyPath := selfSigned(t, "pc.tailnet.example")
	src := certs.New(t.TempDir(), nil, slog.New(slog.DiscardHandler))
	if err := src.UseFiles(certPath, keyPath); err != nil {
		t.Fatal(err)
	}
	cfg := src.TLSConfig()
	if names := src.Names(); len(names) != 1 || names[0] != "pc.tailnet.example" {
		t.Fatalf("names = %v, want the one non-wildcard name", names)
	}

	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS != nil {
			io.WriteString(w, "secure")
			return
		}
		io.WriteString(w, "plain")
	})}
	go srv.Serve(DualListener(inner, cfg))
	t.Cleanup(func() { srv.Close() })

	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	for scheme, want := range map[string]string{"http": "plain", "https": "secure"} {
		res, err := client.Get(scheme + "://" + inner.Addr().String() + "/")
		if err != nil {
			t.Fatalf("%s: %v", scheme, err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if string(body) != want {
			t.Errorf("%s answered %q, want %q", scheme, body, want)
		}
	}
}

func TestPairingLinksUseTheCertificateName(t *testing.T) {
	h := newHarness(t, config.Config{Addr: "0.0.0.0:4321"}, nil)
	h.server.token = "s3cret"
	src := certs.New(t.TempDir(), nil, slog.New(slog.DiscardHandler))
	if err := src.UseFiles(selfSigned(t, "pc.tailnet.example")); err != nil {
		t.Fatal(err)
	}
	h.server.SetCerts(src)
	urls := h.server.accessURLs()
	if len(urls) == 0 || urls[0] != "https://pc.tailnet.example:4321/?token=s3cret" {
		t.Fatalf("urls = %v", urls)
	}
}
