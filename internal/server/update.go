package server

import "net/http"

func (s *Server) updateStatus(w http.ResponseWriter, r *http.Request) {
	if s.updater == nil {
		send(w, http.StatusServiceUnavailable, map[string]any{"error": "updater unavailable"})
		return
	}
	send(w, http.StatusOK, s.updater.Status())
}

func (s *Server) updateCheck(w http.ResponseWriter, r *http.Request) {
	if s.updater == nil {
		send(w, http.StatusServiceUnavailable, map[string]any{"error": "updater unavailable"})
		return
	}
	st, err := s.updater.Check(r.Context())
	if err != nil {
		s.log.Warn("update check", "err", err)
	}
	send(w, http.StatusOK, st)
}

// updateApply starts the download and returns; the page polls updateStatus and
// reloads once the new version answers.
func (s *Server) updateApply(w http.ResponseWriter, r *http.Request) {
	if s.updater == nil {
		send(w, http.StatusServiceUnavailable, map[string]any{"error": "updater unavailable"})
		return
	}
	if err := s.updater.Apply(); err != nil {
		send(w, http.StatusConflict, map[string]any{"error": err.Error()})
		return
	}
	send(w, http.StatusAccepted, s.updater.Status())
}
