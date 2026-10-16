package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"kuro/internal/config"
	"kuro/internal/store"
)

// A paired device cannot sign the host's trackers out or swap its keys.
func TestTrackerLogoutAndKeysAreHostOnly(t *testing.T) {
	h := newHarness(t, config.Config{Addr: "127.0.0.1:4321"}, nil)
	h.server.token = "s3cret"
	for _, target := range []string{"/api/auth/logout", "/api/mal/auth/logout", "/api/trackers"} {
		d := newDevice(t, h)
		res := d.post(target+"?token=s3cret", `{"provider":"anilist","clientId":"x"}`)
		if res.StatusCode != http.StatusForbidden {
			t.Errorf("%s from a phone answered %d, want 403", target, res.StatusCode)
		}
	}
}

// A range that ends before it starts is a bad request, not a crash.
func TestDownloadAllRefusesAnEmptyRange(t *testing.T) {
	h, _ := downloadsHarness(t)
	for _, body := range []string{`{"animeId":1,"from":20,"to":0}`, `{"animeId":1,"from":5,"to":3}`} {
		if code, _ := h.call(t, "POST", "/api/download/all", body); code != http.StatusBadRequest {
			t.Errorf("%s answered %d, want 400", body, code)
		}
	}
}

// The catalogue being down is a 502 like everywhere else; only a sort kuro does not know is a 400.
func TestDiscoverTellsABadSortFromAnOutage(t *testing.T) {
	down := newHarness(t, config.Config{}, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	})
	if res, _ := down.do(t, "GET", "/api/discover?sort=trending"); res.StatusCode != http.StatusBadGateway {
		t.Errorf("outage answered %d, want 502", res.StatusCode)
	}
	if res, _ := down.do(t, "GET", "/api/discover?sort=sideways"); res.StatusCode != http.StatusBadRequest {
		t.Errorf("unknown sort answered %d, want 400", res.StatusCode)
	}
}

// Every setting with a fixed vocabulary is checked, and numbers must be numbers.
func TestPrefsRefuseTyposInEverySetting(t *testing.T) {
	h := newHarness(t, config.Config{}, nil)
	for key, bad := range map[string]string{
		"cache.autodelete": "Now", "playback.voice": "loud", "downloads.while_watching": "stop",
		"window.mode": "big", "cache.budget_bytes": "ten gigs",
	} {
		if res, _ := h.postJSON(t, "/api/prefs", map[string]any{"key": key, "value": bad}); res.StatusCode != http.StatusBadRequest {
			t.Errorf("%s=%q answered %d, want 400", key, bad, res.StatusCode)
		}
	}
	for key, good := range map[string]string{"cache.autodelete": "keep2", "playback.voice": "high", "cache.budget_bytes": "1000"} {
		if res, body := h.postJSON(t, "/api/prefs", map[string]any{"key": key, "value": good}); res.StatusCode != http.StatusOK {
			t.Errorf("%s=%q answered %d %v", key, good, res.StatusCode, body)
		}
	}
}

// A device the host turned down is still turned down without its cookie: its address answers for it.
func TestDeniedDeviceCannotAskAgainByDroppingItsCookie(t *testing.T) {
	h := approvalHarness(t, approvalOnce)
	d := newDevice(t, h)
	d.get("/?token=s3cret", true)
	id := h.devices(t)[0]["id"].(string)
	h.host(t, "POST", "/api/access/devices/"+id, `{"status":"denied"}`)

	fresh := newDevice(t, h)
	res := fresh.get("/?token=s3cret", true)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d", res.StatusCode)
	}
	if list := h.devices(t); len(list) != 1 || list[0]["status"] != store.DeviceDenied {
		t.Fatalf("devices = %v, want the one denial and no new request", list)
	}
}

// A library folder already configured stays even when its drive is unplugged; only a new one is checked.
func TestOfflineLibraryFolderDoesNotBlockOtherEdits(t *testing.T) {
	h, _, _, dir := localHarness(t)
	paths := func(list ...string) string { b, _ := json.Marshal(map[string]any{"paths": list}); return string(b) }
	if code, body := h.call(t, "POST", "/api/local/paths", paths(dir)); code != http.StatusOK {
		t.Fatalf("saving a good folder answered %d %v", code, body)
	}
	// Configured, then gone: a flash drive. Later configured by hand in the setting, as a scan would leave it.
	gone := dir + "-gone"
	configured, _ := json.Marshal([]string{dir, gone})
	h.store.SetSetting(t.Context(), "library.paths", string(configured))

	other := t.TempDir()
	code, body := h.call(t, "POST", "/api/local/paths", paths(dir, gone, other))
	if code != http.StatusOK || len(body["paths"].([]any)) != 3 {
		t.Fatalf("adding beside an offline folder answered %d %v", code, body)
	}
	if code, body := h.call(t, "POST", "/api/local/paths", paths(dir, gone+"-new")); code != http.StatusBadRequest {
		t.Fatalf("a new missing folder answered %d %v, want 400", code, body)
	}
}

func (d *device) post(target, body string) *http.Response {
	d.t.Helper()
	req := httptest.NewRequest("POST", target, strings.NewReader(body))
	req.Host, req.RemoteAddr = "192.168.1.10:4321", phone
	req.Header.Set("Content-Type", "application/json")
	for k, v := range d.cookies {
		req.AddCookie(&http.Cookie{Name: k, Value: v})
	}
	rec := httptest.NewRecorder()
	d.h.handler.ServeHTTP(rec, req)
	return rec.Result()
}
