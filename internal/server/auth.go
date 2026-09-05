package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strconv"

	"kuro/internal/store"
)

// anilistCreds prefers what was entered in the app, since kuro ships as a binary;
// the config file is still read for an install already set up that way.
func (s *Server) anilistCreds(ctx context.Context) (id, secret string) {
	id, _ = s.store.Setting(ctx, "anilist.client_id")
	if id == "" {
		return s.cfg.AniList.ClientID, s.cfg.AniList.ClientSecret
	}
	secret, _ = s.store.Setting(ctx, "anilist.client_secret")
	return id, secret
}

func (s *Server) authLogin(w http.ResponseWriter, r *http.Request) {
	clientID, _ := s.anilistCreds(r.Context())
	if clientID == "" {
		send(w, http.StatusPreconditionFailed, map[string]any{
			"error":    "add your AniList client id and secret in Settings first",
			"register": "https://anilist.co/settings/developer",
			"redirect": s.cfg.RedirectURI(),
		})
		return
	}

	state := randomHex()
	if err := s.store.SetSetting(r.Context(), "anilist.state", state); err != nil {
		s.fail(w, "auth state", err)
		return
	}
	http.Redirect(w, r, s.anilist.AuthorizeURL(clientID, s.cfg.RedirectURI(), state), http.StatusFound)
}

func (s *Server) authCallback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// AniList forwards state untouched but never validates it.
	want, _ := s.store.Setting(ctx, "anilist.state")
	if got := r.URL.Query().Get("state"); want == "" || got != want {
		http.Error(w, "state mismatch", http.StatusBadRequest)
		return
	}
	s.store.SetSetting(ctx, "anilist.state", "")

	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "no code returned", http.StatusBadRequest)
		return
	}

	clientID, clientSecret := s.anilistCreds(ctx)
	token, err := s.anilist.Exchange(ctx, clientID, clientSecret, s.cfg.RedirectURI(), code)
	if err != nil {
		s.log.Error("token exchange", "err", err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	s.anilist.SetToken(token.AccessToken)

	viewer, err := s.anilist.Viewer(ctx)
	if err != nil {
		s.log.Error("viewer", "err", err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	err = s.store.SetSettings(ctx, map[string]string{
		"anilist.token":      token.AccessToken,
		"anilist.expires_at": strconv.FormatInt(token.ExpiresAt().Unix(), 10),
		"anilist.user_id":    strconv.Itoa(viewer.ID),
		"anilist.user_name":  viewer.Name,
		"anilist.score_fmt":  viewer.MediaListOptions.ScoreFormat,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.log.Info("anilist connected", "user", viewer.Name, "id", viewer.ID)
	http.Redirect(w, r, "/settings?connected=anilist", http.StatusFound)
}

// authLogout forgets the account, leaving the client ID so it can reconnect.
// AniList has no revocation endpoint, so this is local; the grant stays on the account.
func (s *Server) authLogout(w http.ResponseWriter, r *http.Request) {
	err := s.store.SetSettings(r.Context(), map[string]string{
		"anilist.token":      "",
		"anilist.expires_at": "",
		"anilist.user_id":    "",
		"anilist.user_name":  "",
		"anilist.score_fmt":  "",
		"anilist.state":      "",
	})
	if err != nil {
		s.fail(w, "anilist logout", err)
		return
	}

	// The live client keeps its own copy; without this it stays authenticated
	// until restart, and requests would carry a token the app has forgotten.
	s.anilist.SetToken("")

	s.log.Info("anilist disconnected")
	send(w, http.StatusOK, map[string]any{"connected": false})
}

func (s *Server) sync(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID, err := s.store.SettingInt(ctx, "anilist.user_id")
	if err != nil || userID == 0 {
		send(w, http.StatusPreconditionFailed, map[string]any{"error": "not connected to anilist"})
		return
	}

	// Merge is the default: it is the choice that cannot lose anything.
	mode := store.ImportMode(r.URL.Query().Get("mode"))
	if mode == "" {
		mode = store.ImportMerge
	}

	result, err := s.importer.Run(ctx, userID, mode)
	if err != nil {
		s.fail(w, "import", err)
		return
	}
	send(w, http.StatusOK, result)
}

func randomHex() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}
