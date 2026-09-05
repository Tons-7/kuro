package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"kuro/internal/library"
)

type playBody struct {
	AnimeID  int    `json:"animeId"`
	Episode  int    `json:"episode"`
	Season   int    `json:"season"`
	InfoHash string `json:"infoHash"`
	External bool   `json:"external"`
	AllowRaw bool   `json:"allowRaw"`
	// Audio overrides the saved sub/dub preference for this play only: it is a
	// "watch this one dubbed" decision, not a change of mind about everything.
	Audio string `json:"audio,omitempty"`
}

func (s *Server) play(w http.ResponseWriter, r *http.Request) {
	if s.playback == nil {
		send(w, http.StatusServiceUnavailable, map[string]any{
			"error": "torrent engine unavailable; check that rqbit is in bin/",
		})
		return
	}

	var body playBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		send(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		return
	}
	if body.AnimeID == 0 {
		send(w, http.StatusBadRequest, map[string]any{"error": "animeId is required"})
		return
	}
	// Season 0 lets the finder read it from the title; forcing 1 rejected every
	// "3rd Season" release.
	prefs := s.preferences(r.Context())
	switch body.Audio {
	case "sub", "dub", "either":
		prefs.Audio = body.Audio
	}

	session, err := s.playback.Start(r.Context(), library.PlayRequest{
		AnimeID:  body.AnimeID,
		Episode:  body.Episode,
		Season:   body.Season,
		InfoHash: body.InfoHash,
		External: body.External,
		AllowRaw: body.AllowRaw,
		Prefs:    prefs,
	})
	if err != nil {
		s.log.Error("play", "anime", body.AnimeID, "episode", body.Episode, "err", err)

		out := map[string]any{"error": err.Error()}
		var missing *library.NoRelease
		if errors.As(err, &missing) && missing.RawOnly {
			out["rawAvailable"] = true
			out["rawTitle"] = missing.RawTitle
		}
		send(w, http.StatusBadGateway, out)
		return
	}
	send(w, http.StatusOK, session)
}

func (s *Server) cacheStatus(w http.ResponseWriter, r *http.Request) {
	usage, err := s.store.CacheUsage(r.Context())
	if err != nil {
		s.fail(w, "cache usage", err)
		return
	}
	send(w, http.StatusOK, usage)
}

func (s *Server) sweepCache(w http.ResponseWriter, r *http.Request) {
	if s.cache == nil {
		send(w, http.StatusServiceUnavailable, map[string]any{"error": "cache manager unavailable"})
		return
	}
	rep, err := s.cache.Sweep(r.Context())
	if err != nil {
		s.fail(w, "cache sweep", err)
		return
	}
	send(w, http.StatusOK, rep)
}

func (s *Server) stopPlayback(w http.ResponseWriter, r *http.Request) {
	if s.player != nil {
		s.player.Stop()
	}

	var body struct {
		AnimeID int `json:"animeId"`
		Episode int `json:"episode"`
	}
	json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body)
	if body.AnimeID > 0 && s.playback != nil {
		s.playback.Suspend(r.Context(), body.AnimeID, body.Episode)
	}
	send(w, http.StatusOK, map[string]any{"stopped": true})
}
