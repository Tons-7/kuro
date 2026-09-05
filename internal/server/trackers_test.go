package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"kuro/internal/config"
	"kuro/internal/store"
)

func (h *harness) doJSON(t *testing.T, method, target string, body any) (*http.Response, map[string]any) {
	t.Helper()

	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, target, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "127.0.0.1:12345"
	req.Host = "127.0.0.1:4321"

	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)

	res := rec.Result()
	var out map[string]any
	if strings.HasPrefix(res.Header.Get("Content-Type"), "application/json") {
		json.NewDecoder(res.Body).Decode(&out)
	}
	return res, out
}

func trackerByName(t *testing.T, h *harness, provider string) map[string]any {
	t.Helper()

	_, body := h.do(t, "GET", "/api/trackers")
	list, _ := body["trackers"].([]any)
	for _, raw := range list {
		if item, ok := raw.(map[string]any); ok && item["provider"] == provider {
			return item
		}
	}
	t.Fatalf("no %q tracker in %v", provider, body)
	return nil
}

func TestTrackerCredentialsAreSavedAndUsed(t *testing.T) {
	h := newHarness(t, config.Config{Addr: "127.0.0.1:4321"}, nil)

	if got := trackerByName(t, h, "anilist")["configured"]; got != false {
		t.Fatalf("configured = %v before anything was entered", got)
	}

	res, _ := h.doJSON(t, "POST", "/api/trackers", map[string]any{
		"provider": "anilist", "clientId": " 4321 ", "clientSecret": "shh",
	})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}

	entry := trackerByName(t, h, "anilist")
	if entry["clientId"] != "4321" {
		t.Fatalf("clientId = %v, want the trimmed value", entry["clientId"])
	}
	if entry["configured"] != true || entry["hasSecret"] != true {
		t.Fatalf("entry = %v", entry)
	}

	// Login can only redirect once credentials exist.
	login, _ := h.do(t, "GET", "/api/auth/login")
	if login.StatusCode != http.StatusFound {
		t.Fatalf("login status = %d, want a redirect once configured", login.StatusCode)
	}
}

// The form cannot show a stored secret, so it sends nothing when untouched;
// treating that as empty used to disconnect the tracker on any unrelated edit.
func TestOmittedSecretKeepsTheStoredOne(t *testing.T) {
	h := newHarness(t, config.Config{Addr: "127.0.0.1:4321"}, nil)

	h.doJSON(t, "POST", "/api/trackers", map[string]any{
		"provider": "anilist", "clientId": "1", "clientSecret": "original",
	})
	h.doJSON(t, "POST", "/api/trackers", map[string]any{
		"provider": "anilist", "clientId": "2",
	})

	if got := trackerByName(t, h, "anilist")["hasSecret"]; got != true {
		t.Fatal("editing the client id discarded the secret")
	}

	kept, _ := h.store.Setting(t.Context(), "anilist.client_secret")
	if kept != "original" {
		t.Fatalf("secret = %q, want it untouched", kept)
	}
}

func TestEmptySecretClearsIt(t *testing.T) {
	h := newHarness(t, config.Config{Addr: "127.0.0.1:4321"}, nil)

	h.doJSON(t, "POST", "/api/trackers", map[string]any{
		"provider": "anilist", "clientId": "1", "clientSecret": "original",
	})
	h.doJSON(t, "POST", "/api/trackers", map[string]any{
		"provider": "anilist", "clientId": "1", "clientSecret": "",
	})

	if got := trackerByName(t, h, "anilist")["hasSecret"]; got != false {
		t.Fatal("an explicit blank should clear the secret")
	}
}

// The secret must never travel back to the browser, whatever the route.
func TestTrackerSecretIsNeverReturned(t *testing.T) {
	h := newHarness(t, config.Config{Addr: "127.0.0.1:4321"}, nil)

	h.doJSON(t, "POST", "/api/trackers", map[string]any{
		"provider": "anilist", "clientId": "1", "clientSecret": "top-secret",
	})

	_, raw := h.do(t, "GET", "/api/trackers")
	if contains(raw, "top-secret") {
		t.Fatalf("secret leaked in %v", raw)
	}

	_, settings := h.do(t, "GET", "/api/settings")
	if settings["anilist.client_secret"] != nil {
		t.Fatal("secret leaked through /api/settings")
	}
	if !store.Secret("anilist.client_secret") {
		t.Fatal("anilist.client_secret is not classified as a secret")
	}
}

func contains(body map[string]any, needle string) bool {
	for _, v := range body {
		switch typed := v.(type) {
		case string:
			if typed == needle {
				return true
			}
		case []any:
			for _, item := range typed {
				if nested, ok := item.(map[string]any); ok && contains(nested, needle) {
					return true
				}
			}
		case map[string]any:
			if contains(typed, needle) {
				return true
			}
		}
	}
	return false
}
