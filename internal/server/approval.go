package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"html"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"kuro/internal/store"
)

const (
	approvalSetting = "access.approval"
	deviceCookie    = "kuro_device"

	approvalOff    = "off"
	approvalOnce   = "once"   // the host accepts a device once and it is remembered
	approvalAlways = "always" // an accepted device asks again after approvalIdle away, or a restart

	approvalIdle = 30 * time.Minute
	// Enough for a household; past it a flood of requests cannot bury the real one.
	maxPending = 20
)

// approvals is the host's say over which paired devices get in. Memory answers
// every request; the database keeps the decisions across restarts.
type approvals struct {
	mu     sync.Mutex
	loaded bool
	mode   string
	status map[string]string
	seen   map[string]time.Time // approvalAlways: an approved device's last request
}

// load must be called with a.mu held.
func (s *Server) loadApprovals(ctx context.Context) {
	a := &s.approvals
	if a.loaded {
		return
	}
	a.mode, a.status, a.seen = approvalOff, map[string]string{}, map[string]time.Time{}
	if s.store == nil {
		a.loaded = true
		return
	}
	if mode, _ := s.store.Setting(ctx, approvalSetting); mode == approvalOnce || mode == approvalAlways {
		a.mode = mode
	}
	devices, err := s.store.Devices(ctx)
	if err != nil {
		// Not marked loaded: fail closed now and read again on the next request.
		s.log.Warn("load devices", "err", err)
		a.mode = approvalOnce
		return
	}
	for _, d := range devices {
		a.status[d.ID] = d.Status
	}
	a.loaded = true
}

// deviceID is what the database holds, so a copied table cannot be replayed as a cookie.
func deviceID(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:16])
}

// admit lets an approved device in (token already checked); any other is recorded and shown the waiting page.
func (s *Server) admit(w http.ResponseWriter, r *http.Request) bool {
	a := &s.approvals
	a.mu.Lock()
	s.loadApprovals(r.Context())
	if a.mode == approvalOff {
		a.mu.Unlock()
		return true
	}

	id := ""
	if c, err := r.Cookie(deviceCookie); err == nil && c.Value != "" {
		id = deviceID(c.Value)
	}
	status, now := a.status[id], time.Now()
	if status == store.DeviceApproved {
		if a.mode == approvalOnce || now.Sub(a.seen[id]) <= approvalIdle {
			a.seen[id] = now
			a.mu.Unlock()
			return true
		}
		status = "" // lapsed: ask again
	}

	navigation := r.Method == http.MethodGet && strings.Contains(r.Header.Get("Accept"), "text/html")
	name := deviceName(r.UserAgent())
	// A cookieless request that is not a page load is a script; it earns no request row.
	if status == "" && (id != "" || navigation) {
		status = s.requestAccess(w, r, id, name)
	}
	a.mu.Unlock()

	switch {
	case r.URL.Path == "/api/access/me":
		send(w, http.StatusOK, map[string]any{"status": status, "name": name})
	case navigation:
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(waitingPage(status, name)))
	default:
		send(w, http.StatusForbidden, map[string]any{
			"error": "this device is waiting for the host to accept it", "approval": status,
		})
	}
	return false
}

// requestAccess records the device as pending, with a new identity if it has none. approvals.mu is held.
func (s *Server) requestAccess(w http.ResponseWriter, r *http.Request, id, name string) string {
	a := &s.approvals
	pending := 0
	for _, st := range a.status {
		if st == store.DevicePending {
			pending++
		}
	}
	if pending >= maxPending {
		return "full"
	}
	if id == "" {
		secret := NewAccessToken()
		http.SetCookie(w, &http.Cookie{
			Name: deviceCookie, Value: secret, Path: "/", MaxAge: 365 * 24 * 3600,
			HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteLaxMode,
		})
		id = deviceID(secret)
	}
	addr, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		addr = r.RemoteAddr
	}
	if err := s.store.RequestAccess(r.Context(), id, name, addr); err != nil {
		s.log.Warn("record access request", "err", err)
		return "full"
	}
	a.status[id] = store.DevicePending
	s.log.Info("device asks for access", "device", name, "addr", addr)
	return store.DevicePending
}

// deviceName is a label the host can recognise, from the User-Agent.
func deviceName(ua string) string {
	pick := func(names ...string) string {
		for i := 0; i < len(names); i += 2 {
			if strings.Contains(ua, names[i]) {
				return names[i+1]
			}
		}
		return ""
	}
	// Order matters: Android says Linux, iPad says Mac OS X, Edge and Opera say Chrome.
	system := pick("Android", "Android", "iPhone", "iPhone", "iPad", "iPad", "Windows", "Windows",
		"Mac OS X", "Mac", "CrOS", "ChromeOS", "Linux", "Linux")
	browser := pick("Firefox", "Firefox", "Edg", "Edge", "OPR", "Opera", "SamsungBrowser", "Samsung Internet",
		"Chrome", "Chrome", "Safari", "Safari")
	switch {
	case system != "" && browser != "":
		return browser + " on " + system
	case system+browser != "":
		return system + browser
	}
	return "Unknown device"
}

// accessMe reaches its handler only for an admitted device; admit answers the rest.
func (s *Server) accessMe(w http.ResponseWriter, r *http.Request) {
	send(w, http.StatusOK, map[string]any{"status": store.DeviceApproved})
}

func (s *Server) accessDevices(w http.ResponseWriter, r *http.Request) {
	devices, err := s.store.Devices(r.Context())
	if err != nil {
		s.fail(w, "devices", err)
		return
	}
	a := &s.approvals
	a.mu.Lock()
	s.loadApprovals(r.Context())
	mode := a.mode
	// Under approvalAlways an accepted device that went quiet has to ask again.
	if mode == approvalAlways {
		for i, d := range devices {
			if d.Status == store.DeviceApproved && time.Since(a.seen[d.ID]) > approvalIdle {
				devices[i].Status = "lapsed"
			}
		}
	}
	a.mu.Unlock()
	send(w, http.StatusOK, map[string]any{
		"mode": mode, "devices": devices, "idleMinutes": int(approvalIdle.Minutes()),
	})
}

func (s *Server) setApproval(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Mode string `json:"mode"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil ||
		(body.Mode != approvalOff && body.Mode != approvalOnce && body.Mode != approvalAlways) {
		send(w, http.StatusBadRequest, map[string]any{"error": "mode is off, once or always"})
		return
	}
	if err := s.store.SetSetting(r.Context(), approvalSetting, body.Mode); err != nil {
		s.fail(w, "set approval", err)
		return
	}
	a := &s.approvals
	a.mu.Lock()
	s.loadApprovals(r.Context())
	a.mode = body.Mode
	a.mu.Unlock()
	send(w, http.StatusOK, map[string]any{"mode": body.Mode})
}

func (s *Server) decideDevice(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Status string `json:"status"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil ||
		(body.Status != store.DeviceApproved && body.Status != store.DeviceDenied) {
		send(w, http.StatusBadRequest, map[string]any{"error": "status is approved or denied"})
		return
	}
	a := &s.approvals
	a.mu.Lock()
	defer a.mu.Unlock()
	s.loadApprovals(r.Context())
	if _, known := a.status[id]; !known {
		send(w, http.StatusNotFound, map[string]any{"error": "no such device"})
		return
	}
	if err := s.store.DecideDevice(r.Context(), id, body.Status); err != nil {
		s.fail(w, "decide device", err)
		return
	}
	a.status[id] = body.Status
	a.seen[id] = time.Now()
	send(w, http.StatusOK, map[string]any{"status": body.Status})
}

func (s *Server) removeDevice(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	a := &s.approvals
	a.mu.Lock()
	defer a.mu.Unlock()
	s.loadApprovals(r.Context())
	if err := s.store.DeleteDevice(r.Context(), id); err != nil {
		s.fail(w, "remove device", err)
		return
	}
	delete(a.status, id)
	delete(a.seen, id)
	send(w, http.StatusOK, map[string]any{"removed": true})
}

// waitingPage is what a device sees until the host decides; self-contained, so the app stays behind the gate.
func waitingPage(status, name string) string {
	title, detail := "Waiting for the host", "Ask the person running kuro to accept this device. This page opens kuro as soon as they do."
	switch status {
	case store.DeviceDenied:
		title, detail = "Access declined", "The host declined this device. They can remove it from Settings to let it ask again."
	case "full":
		title, detail = "Too many requests", "Too many devices are waiting. Ask the host to clear the list in Settings, then reload."
	}
	return `<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1"><meta name="theme-color" content="#0b0d12">
<title>kuro</title><style>
body{margin:0;min-height:100vh;display:grid;place-items:center;background:#0b0d12;color:#d6dbe6;font:16px/1.5 system-ui,sans-serif}
main{max-width:22rem;padding:2rem;text-align:center}h1{font-size:1.25rem;margin:0 0 .5rem}p{margin:0;color:#8b93a7}
code{display:inline-block;margin-top:1rem;padding:.25rem .6rem;border-radius:.4rem;background:#151822;color:#d6dbe6}
</style></head><body><main><h1>` + title + `</h1><p>` + detail + `</p><code>` + html.EscapeString(name) + `</code></main>
<script>setInterval(async()=>{try{const r=await fetch('/api/access/me',{cache:'no-store'});const s=(await r.json()).status;
if(s!=='` + status + `')location.reload()}catch{}},3000)</script></body></html>`
}
