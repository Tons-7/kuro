package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	qrcode "github.com/yeqown/go-qrcode/v2"

	"kuro/internal/firewall"
)

const (
	accessCookie  = "kuro_access"
	accessSetting = "access.token"
	// Whether the listener is open to the network. Kept in the database rather
	// than config.toml so it can be a switch in the app.
	lanSetting = "access.lan"
)

// OnRebind hands the server a way to move its listener, so opening the app to
// the network does not mean editing a file and restarting.
func (s *Server) OnRebind(rebind func(addr string) error) { s.rebind = rebind }

// LANAddr is the address to listen on when the app is open to the network: the
// port stays whatever was configured, only the host changes.
func LANAddr(configured string) string {
	_, port, err := net.SplitHostPort(configured)
	if err != nil {
		return "0.0.0.0:4321"
	}
	return net.JoinHostPort("0.0.0.0", port)
}

// NewAccessToken is generated once on first run and stored; callers persist it
// so paired devices survive a restart.
func NewAccessToken() string {
	b := make([]byte, 24)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func isLoopback(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// loopbackHost reports whether the request was addressed to this machine by a
// loopback name. A page on any other name that resolves to 127.0.0.1 reaches
// the same socket, so the address alone cannot earn the unguarded exemption.
func loopbackHost(host string) bool {
	h, _, err := net.SplitHostPort(host)
	if err != nil {
		h = host
	}
	h = strings.ToLower(strings.Trim(h, "[]"))
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// presented pulls the token from whichever channel the caller could use: media
// elements and mpv cannot set headers, so query parameter and cookie must work.
func presented(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	if t := r.URL.Query().Get("token"); t != "" {
		return t
	}
	if c, err := r.Cookie(accessCookie); err == nil {
		return c.Value
	}
	return ""
}

// guard leaves loopback alone so the local browser and mpv need no setup, and
// requires the token from everything else, failing closed when it is missing.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Any open page can POST here, and loopback needs no token.
		if !safeMethod(r.Method) && !sameOrigin(r) {
			send(w, http.StatusForbidden, map[string]any{"error": "cross-site request refused"})
			return
		}
		if isLoopback(r.RemoteAddr) && loopbackHost(r.Host) {
			next.ServeHTTP(w, r)
			return
		}
		// Closing the listener leaves kept-alive connections open; network off
		// has to shut out the devices already on them.
		if !isLoopback(r.RemoteAddr) && s.rebind != nil && !s.bound() {
			send(w, http.StatusForbidden, map[string]any{"error": "network access is off"})
			return
		}
		token := s.accessToken()
		if token == "" {
			send(w, http.StatusServiceUnavailable, map[string]any{
				"error": "remote access is not configured",
			})
			return
		}

		if subtle.ConstantTimeCompare([]byte(presented(r)), []byte(token)) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="kuro"`)
			send(w, http.StatusUnauthorized, map[string]any{"error": "invalid or missing token"})
			return
		}

		if r.URL.Query().Get("token") != "" {
			http.SetCookie(w, &http.Cookie{
				Name: accessCookie, Value: token, Path: "/",
				MaxAge: 365 * 24 * 3600, HttpOnly: true, SameSite: http.SameSiteLaxMode,
			})
		}
		next.ServeHTTP(w, r)
	})
}

// hostOnly keeps a route to the machine kuro runs on: a paired device holds
// the token, but must not reconfigure the host or link its accounts.
func hostOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isLoopback(r.RemoteAddr) || !loopbackHost(r.Host) {
			send(w, http.StatusForbidden, map[string]any{"error": "change this from the host machine"})
			return
		}
		next(w, r)
	}
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// sameOrigin: kuro's own page, or a non-browser client (no Origin).
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		site := r.Header.Get("Sec-Fetch-Site")
		return site == "" || site == "same-origin" || site == "none"
	}
	u, err := url.Parse(origin)
	return err == nil && strings.EqualFold(u.Host, r.Host)
}

// lanAddrs lists the addresses a phone on the same network can actually reach.
func lanAddrs() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}

	var out []string
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipnet.IP.To4()
			if ip == nil || !ip.IsPrivate() {
				continue
			}
			out = append(out, ip.String())
		}
	}
	return out
}

func (s *Server) port() string {
	_, port, err := net.SplitHostPort(s.cfg.Addr)
	if err != nil {
		return "4321"
	}
	return port
}

// bound reports whether the listener can be reached from off the machine.
func (s *Server) bound() bool {
	host, _, err := net.SplitHostPort(s.listening())
	if err != nil {
		return false
	}
	if host == "" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsUnspecified() || !ip.IsLoopback())
}

func (s *Server) accessToken() string {
	s.tokenMu.RLock()
	defer s.tokenMu.RUnlock()
	return s.token
}

func (s *Server) accessURLs() []string {
	var urls []string
	token := s.accessToken()
	for _, host := range lanAddrs() {
		urls = append(urls, fmt.Sprintf("http://%s:%s/?token=%s", host, s.port(), token))
	}
	return urls
}

// PairingURLs is empty unless the listener is bound beyond loopback, which is
// the only case where a phone could use them.
func (s *Server) PairingURLs() []string {
	if !s.bound() {
		return nil
	}
	return s.accessURLs()
}

func (s *Server) access(w http.ResponseWriter, r *http.Request) {
	// The switch, firewall and sign-out are host-only routes; a paired device
	// is not offered controls that would only answer 403.
	host := isLoopback(r.RemoteAddr) && loopbackHost(r.Host)
	send(w, http.StatusOK, map[string]any{
		"listening": s.listening(),
		"reachable": s.bound(),
		"token":     s.accessToken(),
		"urls":      s.accessURLs(),
		"canSwitch": s.rebind != nil && host,
		"host":      host,
	})
}

// listening is where the app is actually bound, which is the configured address
// unless the switch below has moved it.
func (s *Server) listening() string {
	if s.lanEnabled(context.Background()) {
		return LANAddr(s.cfg.Addr)
	}
	return s.cfg.Addr
}

func (s *Server) lanEnabled(ctx context.Context) bool {
	if s.store == nil {
		return false
	}
	v, _ := s.store.Setting(ctx, lanSetting)
	return v == "true"
}

// setAccessNetwork opens or closes the app to the network and moves the listener.
// Restricted to loopback: a paired device must not open the port for the owner.
func (s *Server) setAccessNetwork(w http.ResponseWriter, r *http.Request) {
	if s.rebind == nil {
		send(w, http.StatusServiceUnavailable, map[string]any{"error": "this build cannot move its listener"})
		return
	}

	var body struct {
		LAN bool `json:"lan"`
	}
	if !decode(w, r, &body) {
		return
	}

	addr := s.cfg.Addr
	if body.LAN {
		addr = LANAddr(s.cfg.Addr)
	}
	if err := s.rebind(addr); err != nil {
		s.fail(w, "move the listener", err)
		return
	}

	// Only recorded once the move worked, so a port already in use leaves the
	// setting saying what is actually true.
	if err := s.store.SetSetting(r.Context(), lanSetting, strconv.FormatBool(body.LAN)); err != nil {
		s.fail(w, "save access setting", err)
		return
	}

	s.log.Info("network access", "on", body.LAN, "listening", addr, "addresses", lanAddrs())
	if body.LAN {
		go s.LogFirewall(context.WithoutCancel(r.Context()))
	}
	send(w, http.StatusOK, map[string]any{
		"listening": addr,
		"reachable": body.LAN,
		"urls":      s.accessURLs(),
	})
}

func (s *Server) firewallStatus(ctx context.Context) (firewall.Status, error) {
	port, _ := strconv.Atoi(s.port())
	exe, _ := os.Executable()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return firewall.Check(ctx, port, exe)
}

// LogFirewall says in the log why a phone would or would not get in, which is
// the first thing to look at when one cannot.
func (s *Server) LogFirewall(ctx context.Context) {
	st, err := s.firewallStatus(ctx)
	if err != nil {
		s.log.Warn("firewall check", "err", err)
		return
	}
	for _, n := range st.Networks {
		s.log.Info("network", "name", n.Name, "interface", n.Interface, "category", n.Category)
	}
	switch {
	case st.Firewall != "":
		s.log.Info("firewall", "running", st.Firewall, "hint", st.Hint, "command", st.Command)
	case !st.Supported:
	case st.Blocked():
		s.log.Warn("firewall blocks kuro.exe; other devices cannot connect until it is allowed in Settings › Watch on your phone")
	case !st.Reachable():
		s.log.Warn("firewall does not allow other devices on this network",
			"public", st.OnPublic(), "portRule", st.PortRule != nil)
	default:
		s.log.Info("firewall allows other devices on this network")
	}
}

func (s *Server) accessFirewall(w http.ResponseWriter, r *http.Request) {
	st, err := s.firewallStatus(r.Context())
	if err != nil {
		s.fail(w, "firewall", err)
		return
	}
	send(w, http.StatusOK, map[string]any{
		"status":    st,
		"reachable": st.Reachable(),
		"blocked":   st.Blocked(),
		"public":    st.OnPublic(),
		"addresses": lanAddrs(),
	})
}

// allowFirewall asks Windows (through its administrator prompt) for kuro's
// port rule. Private networks only unless public is asked for explicitly.
func (s *Server) allowFirewall(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Public bool `json:"public"`
	}
	if !decode(w, r, &body) {
		return
	}
	port, _ := strconv.Atoi(s.port())
	exe, _ := os.Executable()
	err := firewall.Allow(r.Context(), port, exe, body.Public)
	if errors.Is(err, firewall.ErrCancelled) {
		send(w, http.StatusConflict, map[string]any{"error": err.Error()})
		return
	}
	if err != nil {
		s.fail(w, "firewall", err)
		return
	}
	s.log.Info("firewall rule added", "rule", firewall.RuleName(port), "public", body.Public)
	s.LogFirewall(r.Context())
	s.accessFirewall(w, r)
}

func (s *Server) openNetworkSettings(w http.ResponseWriter, r *http.Request) {
	if err := firewall.OpenNetworkSettings(); err != nil {
		s.fail(w, "open network settings", err)
		return
	}
	send(w, http.StatusOK, map[string]any{"opened": true})
}

// accessQR renders the pairing URL as SVG. Typing a 32-character token into a
// TV remote is not a thing anyone should have to do.
func (s *Server) accessQR(w http.ResponseWriter, r *http.Request) {
	target := r.URL.Query().Get("url")
	if target == "" {
		urls := s.accessURLs()
		if len(urls) == 0 {
			send(w, http.StatusPreconditionFailed, map[string]any{
				"error": "no private network address found",
			})
			return
		}
		target = urls[0]
	}

	svg, err := qrSVG(target)
	if err != nil {
		s.fail(w, "qr", err)
		return
	}

	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(svg)
}

// rotate invalidates every paired device. Restricted to loopback so a stolen
// token cannot be used to lock the owner out.
func (s *Server) rotateAccess(w http.ResponseWriter, r *http.Request) {
	token := NewAccessToken()
	if err := s.store.SetSetting(r.Context(), accessSetting, token); err != nil {
		s.fail(w, "rotate access token", err)
		return
	}
	s.tokenMu.Lock()
	s.token = token
	s.tokenMu.Unlock()

	send(w, http.StatusOK, map[string]any{"token": token, "urls": s.accessURLs()})
}

type bitmapWriter struct{ cells [][]bool }

func (b *bitmapWriter) Write(m qrcode.Matrix) error { b.cells = m.Bitmap(); return nil }
func (b *bitmapWriter) Close() error                { return nil }

// qrCells returns the modules row-major, matching Matrix.Bitmap.
func qrCells(content string) ([][]bool, error) {
	code, err := qrcode.New(content)
	if err != nil {
		return nil, err
	}

	var bm bitmapWriter
	if err := code.Save(&bm); err != nil {
		return nil, err
	}
	if len(bm.cells) == 0 {
		return nil, fmt.Errorf("empty qr matrix")
	}
	return bm.cells, nil
}

// qrSVG keeps the output vector and free of image encoders: the browser scales
// it to whatever the screen is.
func qrSVG(content string) ([]byte, error) {
	cells, err := qrCells(content)
	if err != nil {
		return nil, err
	}

	const quiet = 4
	size := len(cells) + quiet*2

	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges">`, size, size)
	fmt.Fprintf(&b, `<rect width="%d" height="%d" fill="#fff"/><path fill="#000" d="`, size, size)

	for y, row := range cells {
		for x, on := range row {
			if on {
				fmt.Fprintf(&b, "M%d %dh1v1h-1z", x+quiet, y+quiet)
			}
		}
	}
	b.WriteString(`"/></svg>`)

	return []byte(b.String()), nil
}
