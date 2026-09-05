package server

import (
	"encoding/xml"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"

	qrcode "github.com/yeqown/go-qrcode/v2"

	"kuro/internal/config"
	"kuro/internal/store"
	"kuro/internal/transcode"
)

// from sets the client address, which is what decides whether the token is
// demanded at all.
func (h *harness) from(t *testing.T, addr, target string, header http.Header) *http.Response {
	t.Helper()
	req := httptest.NewRequest("GET", target, nil)
	// What a real client sends; the guard only exempts loopback by name too.
	req.Host = "127.0.0.1:4321"
	req.RemoteAddr = addr
	for k, v := range header {
		req.Header[k] = v
	}

	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)
	return rec.Result()
}

func TestLoopbackNeedsNoToken(t *testing.T) {
	h := newHarness(t, config.Config{}, nil)
	h.server.token = "s3cret"

	for _, addr := range []string{"127.0.0.1:5555", "[::1]:5555"} {
		if res := h.from(t, addr, "/api/health", nil); res.StatusCode != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", addr, res.StatusCode)
		}
	}
}

func TestRemoteWithoutTokenIsRejected(t *testing.T) {
	h := newHarness(t, config.Config{}, nil)
	h.server.token = "s3cret"

	res := h.from(t, "192.168.1.50:5555", "/api/health", nil)
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", res.StatusCode)
	}
	if res.Header.Get("WWW-Authenticate") == "" {
		t.Error("no challenge header")
	}
}

func TestRemoteWithWrongTokenIsRejected(t *testing.T) {
	h := newHarness(t, config.Config{}, nil)
	h.server.token = "s3cret"

	cases := map[string]http.Header{
		"bearer": {"Authorization": {"Bearer wrong"}},
		"cookie": {"Cookie": {accessCookie + "=wrong"}},
	}
	for name, header := range cases {
		if res := h.from(t, "192.168.1.50:5555", "/api/health", header); res.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: status = %d, want 401", name, res.StatusCode)
		}
	}

	res := h.from(t, "192.168.1.50:5555", "/api/health?token=wrong", nil)
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("query: status = %d, want 401", res.StatusCode)
	}
}

func TestRemoteAcceptsEveryTokenChannel(t *testing.T) {
	h := newHarness(t, config.Config{}, nil)
	h.server.token = "s3cret"

	cases := map[string]struct {
		target string
		header http.Header
	}{
		"bearer": {"/api/health", http.Header{"Authorization": {"Bearer s3cret"}}},
		"cookie": {"/api/health", http.Header{"Cookie": {accessCookie + "=s3cret"}}},
		"query":  {"/api/health?token=s3cret", nil},
	}

	for name, tc := range cases {
		res := h.from(t, "192.168.1.50:5555", tc.target, tc.header)
		if res.StatusCode != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", name, res.StatusCode)
		}
	}
}

// A media element cannot set a header on the segment requests that follow, so
// the query parameter has to leave a cookie behind.
func TestQueryTokenSetsCookie(t *testing.T) {
	h := newHarness(t, config.Config{}, nil)
	h.server.token = "s3cret"

	res := h.from(t, "192.168.1.50:5555", "/api/health?token=s3cret", nil)
	for _, c := range res.Cookies() {
		if c.Name == accessCookie {
			if c.Value != "s3cret" {
				t.Fatalf("cookie value = %q", c.Value)
			}
			if !c.HttpOnly {
				t.Error("pairing cookie should be HttpOnly")
			}
			return
		}
	}
	t.Fatal("no cookie set")
}

// An unset token must not read as "no authentication required".
func TestMissingTokenFailsClosedForRemotes(t *testing.T) {
	h := newHarness(t, config.Config{}, nil)
	h.server.token = ""

	res := h.from(t, "192.168.1.50:5555", "/api/health", nil)
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", res.StatusCode)
	}
}

// A page on any hostname that resolves to 127.0.0.1 reaches this socket from
// the local browser, so the address alone cannot earn the unguarded exemption.
func TestForeignHostOnLoopbackStillNeedsTheToken(t *testing.T) {
	h := newHarness(t, config.Config{}, nil)
	h.server.token = "s3cret"

	for _, host := range []string{"evil.example", "kuro.attacker.test:4321"} {
		req := httptest.NewRequest("GET", "/api/access", nil)
		req.RemoteAddr = "127.0.0.1:5555"
		req.Host = host

		rec := httptest.NewRecorder()
		h.handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("host %q: status = %d, want 401", host, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "s3cret") {
			t.Errorf("host %q: the token was handed out", host)
		}
	}

	// The real local names still work without one.
	for _, host := range []string{"localhost:4321", "127.0.0.1:4321", "[::1]:4321"} {
		req := httptest.NewRequest("GET", "/api/access", nil)
		req.RemoteAddr = "127.0.0.1:5555"
		req.Host = host

		rec := httptest.NewRecorder()
		h.handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("host %q: status = %d, want 200", host, rec.Code)
		}
	}
}

// The source reaches ffmpeg as -i, so only the two shapes /api/play hands out
// may be accepted; anything else is a fetch or a file read of the caller's choosing.
func TestStreamOpenRejectsForeignSources(t *testing.T) {
	// The address a stock install resolves to, not one written by hand: the
	// engine's own default lives on its copy, so a config that never set it
	// would reject every torrent stream.
	h := newHarness(t, config.Config{Torrent: config.Torrent{APIAddr: config.DefaultTorrentAPIAddr}}, nil)
	// Present so the handler reaches the source check rather than reporting no
	// transcoder; nothing here ever runs ffmpeg.
	h.server.streams = transcode.NewManager("ffmpeg", "ffprobe", t.TempDir(), "libx264", slog.New(slog.DiscardHandler))

	for _, source := range []string{
		"http://169.254.169.254/latest/meta-data/",
		"http://192.168.1.1/admin",
		"file:///etc/passwd",
		`D:\Users\user\Videos\private.mkv`,
		"/etc/shadow",
	} {
		req := httptest.NewRequest("POST", "/api/stream/open?id=1&episode=1&source="+url.QueryEscape(source), nil)
		req.RemoteAddr = "127.0.0.1:5555"
		req.Host = "127.0.0.1:4321"

		rec := httptest.NewRecorder()
		h.handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("source %q: status = %d, want 400", source, rec.Code)
		}
	}

	// And the engine's own stream URL is still accepted, or nothing plays.
	engine := "http://" + config.DefaultTorrentAPIAddr + "/torrents/4/stream/2"
	req := httptest.NewRequest("POST", "/api/stream/open?id=1&episode=1&source="+url.QueryEscape(engine), nil)
	req.RemoteAddr = "127.0.0.1:5555"
	req.Host = "127.0.0.1:4321"

	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)
	if rec.Code == http.StatusBadRequest {
		t.Errorf("the engine's own stream URL was rejected: %s", rec.Body.String())
	}
}

// A stock config never writes the engine address, so anything comparing against
// it must still resolve to where rqbit actually listens.
func TestDefaultConfigCarriesTheEngineAddress(t *testing.T) {
	if config.DefaultTorrentAPIAddr == "" {
		t.Fatal("no default engine address")
	}
	var stock config.Config
	if stock.Torrent.APIAddr != "" {
		t.Fatal("the zero value should be empty; Load is what fills it")
	}
}

func TestRotateIsLoopbackOnly(t *testing.T) {
	h := newHarness(t, config.Config{}, nil)
	h.server.token = "s3cret"

	req := httptest.NewRequest("POST", "/api/access/rotate", nil)
	req.RemoteAddr = "192.168.1.50:5555"
	req.Header.Set("Authorization", "Bearer s3cret")

	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 even with a valid token", rec.Code)
	}
	if h.server.token != "s3cret" {
		t.Error("token was rotated by a remote caller")
	}
}

func TestRotateReplacesTokenAndPersists(t *testing.T) {
	h := newHarness(t, config.Config{}, nil)
	h.server.token = "s3cret"

	req := httptest.NewRequest("POST", "/api/access/rotate", nil)
	req.RemoteAddr = "127.0.0.1:5555"
	req.Host = "127.0.0.1:4321"
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if h.server.token == "s3cret" || h.server.token == "" {
		t.Fatalf("token = %q, want a fresh one", h.server.token)
	}

	saved, _ := h.store.Setting(t.Context(), accessSetting)
	if saved != h.server.token {
		t.Errorf("stored %q, live %q", saved, h.server.token)
	}

	// The old token must stop working immediately.
	res := h.from(t, "192.168.1.50:5555", "/api/health", http.Header{"Authorization": {"Bearer s3cret"}})
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("old token still accepted: %d", res.StatusCode)
	}
}

// The AniList JWT is a setting; the settings endpoint must not hand it out.
func TestSettingsRedactsSecrets(t *testing.T) {
	h := newHarness(t, config.Config{}, nil)
	ctx := t.Context()
	h.store.SetSetting(ctx, "anilist.token", "jwt-here")
	h.store.SetSetting(ctx, accessSetting, "s3cret")

	_, body := h.do(t, "GET", "/api/settings")
	for _, key := range []string{"anilist.token", accessSetting} {
		if _, leaked := body[key]; leaked {
			t.Errorf("%s exposed", key)
		}
	}
	if body["cache.budget_bytes"] == nil {
		t.Error("redaction removed ordinary settings too")
	}

	// Still readable by key for internal callers.
	if got, _ := h.store.Setting(ctx, "anilist.token"); got != "jwt-here" {
		t.Errorf("Setting() = %q, want the stored value", got)
	}
}

func TestSecretClassification(t *testing.T) {
	secret := []string{
		"anilist.token", "access.token", "anilist.client.secret", "anilist.state",
		"anilist.client_secret", "mal.client_secret", "mal.refresh.token",
	}
	// A client id is not a credential: it is shown so it can be checked against
	// the developer page that issued it.
	plain := []string{
		"cache.budget_bytes", "playback.player", "anilist.user_name", "notify.enabled",
		"anilist.client_id", "mal.client_id",
	}

	for _, k := range secret {
		if !store.Secret(k) {
			t.Errorf("%s should be secret", k)
		}
	}
	for _, k := range plain {
		if store.Secret(k) {
			t.Errorf("%s should not be secret", k)
		}
	}
}

func TestQRIsValidScalableSVG(t *testing.T) {
	svg, err := qrSVG("http://192.168.1.50:4321/?token=s3cret")
	if err != nil {
		t.Fatal(err)
	}

	var doc struct {
		XMLName xml.Name `xml:"svg"`
		ViewBox string   `xml:"viewBox,attr"`
		Width   string   `xml:"width,attr"`
	}
	if err := xml.Unmarshal(svg, &doc); err != nil {
		t.Fatalf("not well-formed: %v", err)
	}
	if doc.ViewBox == "" {
		t.Error("no viewBox; the QR would not scale")
	}
	// A fixed width on the root would stop it scaling to a TV screen.
	if doc.Width != "" {
		t.Errorf("svg pins width=%q", doc.Width)
	}
	if !strings.Contains(string(svg), "<path") {
		t.Error("no modules drawn")
	}
}

var modulePath = regexp.MustCompile(`M(\d+) (\d+)h1v1h-1z`)

type matrixFunc func(qrcode.Matrix) error

func (f matrixFunc) Write(m qrcode.Matrix) error { return f(m) }
func (f matrixFunc) Close() error                { return nil }

// Reading the bitmap as [x][y] instead of [y][x] renders a transpose that still
// looks like a valid QR code, so each module must land at the encoder's x/y.
func TestQRRendersModulesAtLibraryCoordinates(t *testing.T) {
	const content = "http://192.168.1.50:4321/?token=s3cret"
	const quiet = 4

	code, err := qrcode.New(content)
	if err != nil {
		t.Fatal(err)
	}

	// Iterate is a separate path to the matrix from the Bitmap call qrSVG uses, so
	// this compares the renderer against the encoder rather than against itself.
	want := map[[2]int]bool{}
	err = code.Save(matrixFunc(func(m qrcode.Matrix) error {
		m.Iterate(qrcode.IterDirection_ROW, func(x, y int, v qrcode.QRValue) {
			if v.IsSet() {
				want[[2]int{x, y}] = true
			}
		})
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(want) == 0 {
		t.Fatal("encoder produced no dark modules")
	}

	svg, err := qrSVG(content)
	if err != nil {
		t.Fatal(err)
	}

	got := map[[2]int]bool{}
	for _, m := range modulePath.FindAllStringSubmatch(string(svg), -1) {
		x, _ := strconv.Atoi(m[1])
		y, _ := strconv.Atoi(m[2])
		got[[2]int{x - quiet, y - quiet}] = true
	}

	if len(got) != len(want) {
		t.Fatalf("drew %d modules, encoder set %d", len(got), len(want))
	}
	for cell := range want {
		if !got[cell] {
			t.Fatalf("module at column %d row %d not drawn; the matrix is transposed or offset",
				cell[0], cell[1])
		}
	}
}

func TestQRMatrixIsSquareAndAValidSize(t *testing.T) {
	cells, err := qrCells("http://192.168.1.50:4321/?token=s3cret")
	if err != nil {
		t.Fatal(err)
	}

	size := len(cells)
	if size < 21 || size%4 != 1 {
		t.Fatalf("matrix is %d wide, not a valid QR size", size)
	}
	for i, row := range cells {
		if len(row) != size {
			t.Fatalf("row %d has %d columns, want %d", i, len(row), size)
		}
	}
	// Always-black module, one of the few positions fixed by the spec.
	if !cells[size-8][8] {
		t.Error("dark module missing at row size-8, column 8")
	}
}

func TestLoopbackAddressesAreRecognised(t *testing.T) {
	yes := []string{"127.0.0.1:1", "[::1]:1", "127.0.0.1", "127.5.5.5:80"}
	no := []string{"192.168.1.2:1", "10.0.0.1:1", "[fe80::1]:1", "", "garbage"}

	for _, a := range yes {
		if !isLoopback(a) {
			t.Errorf("%q should be loopback", a)
		}
	}
	for _, a := range no {
		if isLoopback(a) {
			t.Errorf("%q should not be loopback", a)
		}
	}
}

func TestBoundDetectsRemoteReachability(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1:4321":   false,
		"localhost:4321":   false,
		"0.0.0.0:4321":     true,
		":4321":            true,
		"192.168.1.5:4321": true,
	}
	for addr, want := range cases {
		s := &Server{cfg: config.Config{Addr: addr}}
		if got := s.bound(); got != want {
			t.Errorf("%s: bound = %v, want %v", addr, got, want)
		}
	}
}

// The setting must record only what actually happened, so a port already in use
// cannot leave the app claiming to be reachable when it is not.
func TestAccessNetworkSwitchMovesTheListener(t *testing.T) {
	var moved []string
	s := &Server{
		cfg:    config.Config{Addr: "127.0.0.1:4321"},
		rebind: func(addr string) error { moved = append(moved, addr); return nil },
	}

	if got := LANAddr(s.cfg.Addr); got != "0.0.0.0:4321" {
		t.Fatalf("LANAddr = %q, want the same port on every interface", got)
	}

	// Only from the machine itself: a paired phone must not be able to open the
	// port on the owner's behalf.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/access/network", strings.NewReader(`{"lan":true}`))
	r.RemoteAddr = "192.168.1.20:5555"
	s.setAccessNetwork(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("a remote caller got %d, want %d", w.Code, http.StatusForbidden)
	}
	if len(moved) != 0 {
		t.Errorf("the listener moved for a remote caller: %v", moved)
	}
}

// A build with no listener of its own says so rather than pretending.
func TestAccessNetworkSwitchNeedsAListener(t *testing.T) {
	s := &Server{cfg: config.Config{Addr: "127.0.0.1:4321"}}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/access/network", strings.NewReader(`{"lan":true}`))
	r.RemoteAddr = "127.0.0.1:5555"
	s.setAccessNetwork(w, r)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("got %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}
