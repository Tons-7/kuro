package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"kuro/internal/library"
	"kuro/internal/store"
	"kuro/internal/torrent"
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
		"lastScan": s.lastScan(),
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

// setLibraryPaths checks the folders before storing them, so a typo shows now, not as an empty scan.
func (s *Server) lastScan() *library.ScanReport {
	if s.scanner == nil {
		return nil
	}
	return s.scanner.LastReport()
}

func (s *Server) setLibraryPaths(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Paths []string `json:"paths"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		send(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		return
	}

	// Only a new folder has to be there: an unplugged drive must not block every other edit.
	var current []string
	if s.scanner != nil {
		current, _ = s.scanner.Roots(r.Context())
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
		if slices.Contains(current, abs) {
			clean = append(clean, abs)
			continue
		}
		info, err := os.Stat(abs)
		switch {
		case err != nil:
			problems[p] = "not readable"
		case !info.IsDir():
			problems[p] = "not a directory"
		case s.cfg.CacheDir != "" && (torrent.Within(s.cfg.CacheDir, abs) || torrent.Within(abs, s.cfg.CacheDir)):
			problems[p] = "holds kuro's own downloads; a library folder must be elsewhere"
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
	// A folder taken out takes its files with it: they must not keep playing or standing in for downloads.
	if _, err := s.store.SweepOutside(r.Context(), clean); err != nil {
		s.log.Warn("retire files outside the library", "err", err)
	}
	send(w, http.StatusOK, map[string]any{"paths": clean})
}

// forgetMissing clears files gone for good; a scan only flags them.
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

// localStream serves a scanned file; ServeContent's range requests make seeking work.
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

	// Only paths this server recorded are servable, so the id cannot be used to read arbitrary files.
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
