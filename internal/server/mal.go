package server

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"kuro/internal/mal"
)

func (s *Server) malLogin(w http.ResponseWriter, r *http.Request) {
	if s.mal == nil || !s.mal.Configured() {
		send(w, http.StatusPreconditionFailed, map[string]any{
			"error":    "add your MyAnimeList client id in Settings first",
			"register": "https://myanimelist.net/apiconfig",
			"redirect": s.cfg.MALRedirectURI(),
		})
		return
	}

	// The verifier has to survive the round trip to MyAnimeList and back, and
	// MAL only supports the plain challenge method, so it is a secret at rest.
	verifier, state := mal.Verifier(), randomHex()
	err := s.store.SetSettings(r.Context(), map[string]string{
		"mal.verifier.token": verifier,
		"mal.state":          state,
	})
	if err != nil {
		s.fail(w, "mal auth state", err)
		return
	}

	http.Redirect(w, r, s.mal.AuthURL(verifier, state, s.cfg.MALRedirectURI()), http.StatusFound)
}

func (s *Server) malCallback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if s.mal == nil {
		http.Error(w, "myanimelist not configured", http.StatusPreconditionFailed)
		return
	}

	want, _ := s.store.Setting(ctx, "mal.state")
	if got := r.URL.Query().Get("state"); want == "" || got != want {
		http.Error(w, "state mismatch", http.StatusBadRequest)
		return
	}

	verifier, _ := s.store.Setting(ctx, "mal.verifier.token")
	code := r.URL.Query().Get("code")
	if code == "" || verifier == "" {
		http.Error(w, "no code returned", http.StatusBadRequest)
		return
	}
	// Both are single use; clearing them now stops a replayed callback.
	s.store.SetSettings(ctx, map[string]string{"mal.state": "", "mal.verifier.token": ""})

	token, err := s.mal.Exchange(ctx, code, verifier, s.cfg.MALRedirectURI())
	if err != nil {
		s.log.Error("mal token exchange", "err", err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	viewer, err := s.mal.Viewer(ctx)
	if err != nil {
		s.log.Error("mal viewer", "err", err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	if err := s.saveMALToken(ctx, token, viewer.Name); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.log.Info("myanimelist connected", "user", viewer.Name)

	// Seeding what MAL already holds keeps the first sync from replaying the
	// whole list back at it.
	go func() {
		if s.malSync == nil {
			return
		}
		// The request is over by the time this runs, so its context is not
		// the one to inherit.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if _, err := s.malSync.Pull(ctx); err != nil {
			s.log.Warn("mal import", "err", err)
		}
	}()

	http.Redirect(w, r, "/settings?connected=mal", http.StatusFound)
}

func (s *Server) saveMALToken(ctx context.Context, t mal.Token, name string) error {
	values := map[string]string{
		"mal.token":         t.Access,
		"mal.refresh.token": t.Refresh,
		"mal.expires_at":    strconv.FormatInt(t.Expires.Unix(), 10),
	}
	if name != "" {
		values["mal.user_name"] = name
	}
	return s.store.SetSettings(ctx, values)
}

// malLogout forgets the account, leaving the client credentials so it can
// reconnect. MAL has no revocation endpoint, so the grant remains until removed.
func (s *Server) malLogout(w http.ResponseWriter, r *http.Request) {
	err := s.store.SetSettings(r.Context(), map[string]string{
		"mal.token":          "",
		"mal.refresh.token":  "",
		"mal.expires_at":     "",
		"mal.user_name":      "",
		"mal.state":          "",
		"mal.verifier.token": "",
	})
	if err != nil {
		s.fail(w, "myanimelist logout", err)
		return
	}

	if s.mal != nil {
		s.mal.SetToken(mal.Token{})
	}

	s.log.Info("myanimelist disconnected")
	send(w, http.StatusOK, map[string]any{"connected": false})
}

func (s *Server) malStatus(w http.ResponseWriter, r *http.Request) {
	if s.mal == nil {
		send(w, http.StatusOK, map[string]any{"configured": false, "connected": false})
		return
	}

	name, _ := s.store.Setting(r.Context(), "mal.user_name")
	token := s.mal.Token()

	body := map[string]any{
		"configured": s.mal.Configured(),
		"connected":  s.mal.Authenticated(),
		"user":       name,
		"redirect":   s.cfg.MALRedirectURI(),
	}
	if !token.Expires.IsZero() {
		body["expiresAt"] = token.Expires.Unix()
	}
	send(w, http.StatusOK, body)
}

func (s *Server) malImport(w http.ResponseWriter, r *http.Request) {
	if s.malSync == nil || s.mal == nil || !s.mal.Authenticated() {
		send(w, http.StatusPreconditionFailed, map[string]any{"error": "not connected to myanimelist"})
		return
	}

	report, err := s.malSync.Pull(r.Context())
	if err != nil {
		s.fail(w, "mal import", err)
		return
	}
	send(w, http.StatusOK, report)
}

func (s *Server) malPush(w http.ResponseWriter, r *http.Request) {
	if s.malSync == nil || s.mal == nil || !s.mal.Authenticated() {
		send(w, http.StatusPreconditionFailed, map[string]any{"error": "not connected to myanimelist"})
		return
	}

	report, err := s.malSync.Run(r.Context())
	if err != nil {
		s.fail(w, "mal sync", err)
		return
	}
	send(w, http.StatusOK, report)
}
