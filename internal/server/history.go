package server

import (
	"net/http"
	"strconv"
	"time"

	"kuro/internal/store"
)

func (s *Server) history(w http.ResponseWriter, r *http.Request) {
	page, err := s.store.History(r.Context(), paging(r))
	if err != nil {
		s.fail(w, "history", err)
		return
	}
	send(w, http.StatusOK, page)
}

func (s *Server) watchStats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.store.WatchStats(r.Context(), time.Now())
	if err != nil {
		s.fail(w, "watch stats", err)
		return
	}
	send(w, http.StatusOK, stats)
}

func (s *Server) forgetHistory(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AnimeID int    `json:"animeId"`
		Episode int    `json:"episode"`
		All     bool   `json:"all"`
		EpKey   string `json:"epKey"`
	}
	if !decode(w, r, &body) {
		return
	}
	if !body.All && body.AnimeID <= 0 {
		send(w, http.StatusBadRequest, map[string]any{
			"error": "animeId is required unless clearing everything",
		})
		return
	}

	key := body.EpKey
	if key == "" && body.Episode > 0 {
		key = epKey(body.Episode)
	}
	if body.All {
		body.AnimeID, key = 0, ""
	}

	removed, err := s.store.ForgetHistory(r.Context(), body.AnimeID, key)
	if err != nil {
		s.fail(w, "forget history", err)
		return
	}
	send(w, http.StatusOK, map[string]any{"removed": removed})
}

func paging(r *http.Request) store.Paging {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	perPage, _ := strconv.Atoi(r.URL.Query().Get("perPage"))
	return store.Paging{Page: page, PerPage: perPage}
}
