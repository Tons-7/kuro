package server

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"

	"kuro/internal/store"
)

// Values are validated against the known set so a typo cannot silently disable
// a feature: an unrecognised Anime4K mode would otherwise fail at playback.
var allowed = map[string][]string{
	"playback.player":       store.Players,
	"playback.anime4k_mode": store.Anime4KModes,
	"playback.anime4k_size": store.Anime4KSizes,
	"audio.prefer":          store.AudioPrefs,
	"display.titles":        store.TitleModes,
	"notify.releases":       {"sub", "dub", "both"},
}

func isBool(def string) bool { return def == "true" || def == "false" }

func (s *Server) getPrefs(w http.ResponseWriter, r *http.Request) {
	animeID, _ := strconv.Atoi(r.URL.Query().Get("anime"))

	prefs, err := s.store.Prefs(r.Context(), animeID)
	if err != nil {
		s.fail(w, "prefs", err)
		return
	}

	body := map[string]any{"effective": prefs.All(), "defaults": store.Defaults}
	if animeID > 0 {
		overrides, err := s.store.AnimePrefs(r.Context(), animeID)
		if err != nil {
			s.fail(w, "anime prefs", err)
			return
		}
		body["overrides"] = overrides
	}
	send(w, http.StatusOK, body)
}

// A setting with no anime id changes the global default; with one it creates a
// per-show override. An empty value removes the override.
func (s *Server) setPref(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AnimeID int    `json:"animeId"`
		Key     string `json:"key"`
		Value   string `json:"value"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		send(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		return
	}

	if _, known := store.Defaults[body.Key]; !known {
		send(w, http.StatusBadRequest, map[string]any{"error": "unknown setting " + body.Key})
		return
	}
	// A boolean setting is read with == "true", so "True" or "yes" would be
	// stored happily and then silently mean off.
	if isBool(store.Defaults[body.Key]) {
		if body.Value != "" && body.Value != "true" && body.Value != "false" {
			send(w, http.StatusBadRequest, map[string]any{
				"error": "invalid value for " + body.Key, "allowed": []string{"true", "false"},
			})
			return
		}
	} else if valid, ok := allowed[body.Key]; ok && body.Value != "" && !slices.Contains(valid, body.Value) {
		send(w, http.StatusBadRequest, map[string]any{
			"error": "invalid value for " + body.Key, "allowed": valid,
		})
		return
	}

	var err error
	if body.AnimeID > 0 {
		err = s.store.SetAnimePref(r.Context(), body.AnimeID, body.Key, body.Value)
	} else {
		err = s.store.SetSetting(r.Context(), body.Key, body.Value)
	}
	if err != nil {
		s.fail(w, "set pref", err)
		return
	}
	send(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) bookmarks(w http.ResponseWriter, r *http.Request) {
	page, err := s.store.Bookmarks(r.Context(), store.ParsePaging(r.URL.Query(), 60, 200))
	if err != nil {
		s.fail(w, "bookmarks", err)
		return
	}
	send(w, http.StatusOK, page)
}

func (s *Server) setBookmark(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AnimeID int `json:"animeId"`
		store.Bookmark
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil || body.AnimeID == 0 {
		send(w, http.StatusBadRequest, map[string]any{"error": "animeId is required"})
		return
	}
	if err := s.store.SetBookmark(r.Context(), body.AnimeID, body.Bookmark); err != nil {
		s.fail(w, "set bookmark", err)
		return
	}
	send(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) recent(w http.ResponseWriter, r *http.Request) {
	page, err := s.store.RecentlyWatched(r.Context(), store.ParsePaging(r.URL.Query(), 30, 100))
	if err != nil {
		s.fail(w, "recently watched", err)
		return
	}
	send(w, http.StatusOK, page)
}

func (s *Server) notifications(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	unread := r.URL.Query().Get("unread") == "true"

	items, err := s.store.Notifications(r.Context(), unread, limit)
	if err != nil {
		s.fail(w, "notifications", err)
		return
	}
	count, _ := s.store.UnreadCount(r.Context())
	send(w, http.StatusOK, map[string]any{"items": items, "unread": count})
}

func (s *Server) markRead(w http.ResponseWriter, r *http.Request) {
	id, ok := notificationID(w, r)
	if !ok {
		return
	}
	if err := s.store.MarkRead(r.Context(), id); err != nil {
		s.fail(w, "mark read", err)
		return
	}
	send(w, http.StatusOK, map[string]any{"ok": true})
}

// notificationID reads ?id=. Zero means every notification, so an unparseable
// one is refused rather than silently clearing the whole list.
func notificationID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := r.URL.Query().Get("id")
	if raw == "" {
		return 0, true
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		send(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return 0, false
	}
	return id, true
}

// deleteNotification removes one by id, or all of them when none is given.
func (s *Server) deleteNotification(w http.ResponseWriter, r *http.Request) {
	id, ok := notificationID(w, r)
	if !ok {
		return
	}

	removed, err := s.store.DeleteNotification(r.Context(), id)
	if err != nil {
		s.fail(w, "delete notification", err)
		return
	}
	send(w, http.StatusOK, map[string]any{"removed": removed})
}

func (s *Server) setFollow(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AnimeID  int    `json:"animeId"`
		Enabled  bool   `json:"enabled"`
		Quality  string `json:"quality"`
		Group    string `json:"group"`
		MaxBytes int64  `json:"maxBytes"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil || body.AnimeID == 0 {
		send(w, http.StatusBadRequest, map[string]any{"error": "animeId is required"})
		return
	}

	err := s.store.SetFollow(r.Context(), store.Follow{
		AnimeID: body.AnimeID, Quality: body.Quality,
		Group: body.Group, MaxBytes: body.MaxBytes,
	}, body.Enabled)
	if err != nil {
		s.fail(w, "follow", err)
		return
	}
	if body.Enabled {
		s.hydrate(r.Context(), []int{body.AnimeID})
	}
	send(w, http.StatusOK, map[string]any{"following": body.Enabled})
}

// hydrate fills in placeholder rows, which otherwise sit titled "Unknown".
func (s *Server) hydrate(ctx context.Context, ids []int) {
	if s.importer == nil {
		return
	}
	missing, err := s.store.Unhydrated(ctx, ids)
	if err != nil || len(missing) == 0 {
		return
	}
	if _, err := s.importer.Hydrate(ctx, missing); err != nil {
		s.log.Warn("hydrate anime", "count", len(missing), "err", err)
	}
}

func (s *Server) checkReleases(w http.ResponseWriter, r *http.Request) {
	if s.watcher == nil {
		send(w, http.StatusServiceUnavailable, map[string]any{"error": "watcher unavailable"})
		return
	}
	rep, err := s.watcher.Poll(r.Context())
	if err != nil {
		s.fail(w, "check releases", err)
		return
	}
	send(w, http.StatusOK, rep)
}
