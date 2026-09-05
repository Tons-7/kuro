package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"kuro/internal/store"
)

func (s *Server) localStats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.store.LocalStats(r.Context())
	if err != nil {
		s.fail(w, "local stats", err)
		return
	}

	roots, err := s.scannerRoots(r)
	if err != nil {
		s.log.Warn("library roots", "err", err)
	}
	send(w, http.StatusOK, map[string]any{
		"stats":    stats,
		"roots":    roots,
		"scanning": s.scanning.Load(),
	})
}

func (s *Server) localFiles(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	animeID, _ := strconv.Atoi(q.Get("anime"))

	page, err := s.store.LocalFiles(r.Context(), animeID,
		q.Get("unmatched") == "true", store.ParsePaging(q, 60, 200))
	if err != nil {
		s.fail(w, "local files", err)
		return
	}
	send(w, http.StatusOK, page)
}

// localScan walks the configured directories. It runs in the background: a
// large library takes minutes and an HTTP request must not hold that open.
func (s *Server) localScan(w http.ResponseWriter, r *http.Request) {
	roots, err := s.scanner.Roots(r.Context())
	if err != nil {
		s.fail(w, "library roots", err)
		return
	}
	if len(roots) == 0 {
		send(w, http.StatusPreconditionFailed, map[string]any{
			"error": "no directories configured; set library.paths first",
		})
		return
	}
	if !s.scanning.CompareAndSwap(false, true) {
		send(w, http.StatusConflict, map[string]any{"error": "a scan is already running"})
		return
	}

	go func() {
		defer s.scanning.Store(false)

		ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
		defer cancel()

		if _, err := s.scanner.Scan(ctx, s.index.get()); err != nil {
			s.log.Error("library scan", "err", err)
		}
	}()

	send(w, http.StatusAccepted, map[string]any{"status": "started", "roots": roots})
}

// setLibraryPaths validates the directories before storing them, so a typo is
// reported now rather than as an empty scan later.
func (s *Server) setLibraryPaths(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Paths []string `json:"paths"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		send(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		return
	}

	clean := make([]string, 0, len(body.Paths))
	problems := map[string]string{}
	for _, p := range body.Paths {
		if p == "" {
			continue
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			problems[p] = err.Error()
			continue
		}
		info, err := os.Stat(abs)
		switch {
		case err != nil:
			problems[p] = "not readable"
		case !info.IsDir():
			problems[p] = "not a directory"
		default:
			clean = append(clean, abs)
		}
	}
	if len(problems) > 0 {
		send(w, http.StatusBadRequest, map[string]any{"error": "unusable paths", "paths": problems})
		return
	}

	encoded, err := json.Marshal(clean)
	if err != nil {
		s.fail(w, "library paths", err)
		return
	}
	if err := s.store.SetSetting(r.Context(), "library.paths", string(encoded)); err != nil {
		s.fail(w, "library paths", err)
		return
	}
	send(w, http.StatusOK, map[string]any{"paths": clean})
}

// forgetMissing clears files that are gone for good. Scanning only flags them,
// so this is the deliberate step that removes them.
func (s *Server) forgetMissing(w http.ResponseWriter, r *http.Request) {
	n, err := s.store.ForgetMissing(r.Context())
	if err != nil {
		s.fail(w, "forget missing", err)
		return
	}
	send(w, http.StatusOK, map[string]any{"forgotten": n})
}

func (s *Server) assignLocalFile(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID      int `json:"id"`
		AnimeID int `json:"animeId"`
		Episode int `json:"episode"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		send(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		return
	}
	if body.ID <= 0 {
		send(w, http.StatusBadRequest, map[string]any{"error": "id is required"})
		return
	}

	if body.AnimeID != 0 {
		if err := s.store.EnsureAnime(r.Context(), body.AnimeID); err != nil {
			s.fail(w, "ensure anime", err)
			return
		}
	}
	if err := s.store.AssignLocalFile(r.Context(), body.ID, body.AnimeID, body.Episode); err != nil {
		s.fail(w, "assign local file", err)
		return
	}
	send(w, http.StatusOK, map[string]any{"id": body.ID, "animeId": body.AnimeID, "episode": body.Episode})
}

// localStream serves a scanned file. http.ServeContent handles range requests,
// which is what makes seeking work in the browser player.
func (s *Server) localStream(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(r.PathValue("id"))
	if id <= 0 {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}

	f, err := s.store.LocalFile(r.Context(), id)
	if err != nil {
		s.log.Error("local file", "err", err)
		http.Error(w, "lookup failed", http.StatusBadGateway)
		return
	}
	if f.ID == 0 {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	// Only paths this server recorded are servable, so the id cannot be used
	// to read arbitrary files.
	file, err := os.Open(f.Path)
	if err != nil {
		http.Error(w, "file unavailable", http.StatusNotFound)
		return
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		http.Error(w, "file unavailable", http.StatusNotFound)
		return
	}

	w.Header().Set("Accept-Ranges", "bytes")
	http.ServeContent(w, r, filepath.Base(f.Path), info.ModTime(), file)
}
