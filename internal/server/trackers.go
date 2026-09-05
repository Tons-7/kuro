package server

import (
	"encoding/json"
	"net/http"
	"strings"
)

type trackerStatus struct {
	Provider   string `json:"provider"`
	Name       string `json:"name"`
	ClientID   string `json:"clientId"`
	HasSecret  bool   `json:"hasSecret"`
	Configured bool   `json:"configured"`
	Connected  bool   `json:"connected"`
	User       string `json:"user,omitempty"`
	Redirect   string `json:"redirect"`
	Register   string `json:"register"`
	// False for MyAnimeList: its public clients authenticate with PKCE alone.
	SecretRequired bool `json:"secretRequired"`
}

func (s *Server) trackers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	anilistID, anilistSecret := s.anilistCreds(ctx)
	anilistUser, _ := s.store.Setting(ctx, "anilist.user_name")
	anilistToken, _ := s.store.Setting(ctx, "anilist.token")

	list := []trackerStatus{{
		Provider:       "anilist",
		Name:           "AniList",
		ClientID:       anilistID,
		HasSecret:      anilistSecret != "",
		Configured:     anilistID != "",
		Connected:      anilistToken != "",
		User:           anilistUser,
		Redirect:       s.cfg.RedirectURI(),
		Register:       "https://anilist.co/settings/developer",
		SecretRequired: true,
	}}

	mal := trackerStatus{
		Provider: "mal",
		Name:     "MyAnimeList",
		Redirect: s.cfg.MALRedirectURI(),
		Register: "https://myanimelist.net/apiconfig",
	}
	if s.mal != nil {
		secret, _ := s.store.Setting(ctx, "mal.client_secret")
		mal.ClientID = s.mal.ClientID()
		mal.HasSecret = secret != ""
		mal.Configured = s.mal.Configured()
		mal.Connected = s.mal.Authenticated()
		mal.User, _ = s.store.Setting(ctx, "mal.user_name")
	}
	list = append(list, mal)

	send(w, http.StatusOK, map[string]any{"trackers": list})
}

func (s *Server) setTracker(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Provider string `json:"provider"`
		ClientID string `json:"clientId"`
		// Omitted keeps the stored secret; empty string clears it.
		ClientSecret *string `json:"clientSecret"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		send(w, http.StatusBadRequest, map[string]any{"error": "malformed body"})
		return
	}

	var prefix string
	switch body.Provider {
	case "anilist":
		prefix = "anilist."
	case "mal":
		prefix = "mal."
	default:
		send(w, http.StatusBadRequest, map[string]any{"error": "unknown provider"})
		return
	}

	ctx := r.Context()
	id := strings.TrimSpace(body.ClientID)
	values := map[string]string{prefix + "client_id": id}
	if body.ClientSecret != nil {
		values[prefix+"client_secret"] = strings.TrimSpace(*body.ClientSecret)
	}
	if err := s.store.SetSettings(ctx, values); err != nil {
		s.fail(w, "save credentials", err)
		return
	}

	if body.Provider == "mal" && s.mal != nil {
		secret, _ := s.store.Setting(ctx, "mal.client_secret")
		s.mal.SetCredentials(id, secret)
	}

	s.log.Info("tracker credentials saved", "provider", body.Provider, "configured", id != "")
	s.trackers(w, r)
}
