package engine

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	"kuro/internal/torrent"
)

// How far a stream reads ahead of the player: a few seconds of a 1080p
// episode, enough to ride out a slow block without starving other files.
const streamReadahead = 16 << 20

func (e *Engine) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]any{"engine": "anacrolix"})
	})
	mux.HandleFunc("GET /torrents", e.started(e.handleList))
	mux.HandleFunc("POST /torrents", e.started(e.handleAdd))
	mux.HandleFunc("GET /torrents/{id}", e.started(e.withEntry(func(w http.ResponseWriter, r *http.Request, en *entry) {
		reply(w, e.detail(en))
	})))
	mux.HandleFunc("GET /torrents/{id}/stats/v1", e.started(e.withEntry(func(w http.ResponseWriter, r *http.Request, en *entry) {
		reply(w, e.stats(en))
	})))
	mux.HandleFunc("GET /torrents/{id}/stream/{file}", e.started(e.withEntry(e.handleStream)))
	mux.HandleFunc("POST /torrents/{id}/pause", e.started(e.withEntry(func(w http.ResponseWriter, r *http.Request, en *entry) {
		e.setPaused(en, true)
		reply(w, struct{}{})
	})))
	mux.HandleFunc("POST /torrents/{id}/start", e.started(e.withEntry(func(w http.ResponseWriter, r *http.Request, en *entry) {
		e.setPaused(en, false)
		reply(w, struct{}{})
	})))
	mux.HandleFunc("POST /torrents/{id}/delete", e.started(e.withEntry(func(w http.ResponseWriter, r *http.Request, en *entry) {
		if err := e.remove(en, true); err != nil {
			fail(w, http.StatusInternalServerError, err)
			return
		}
		reply(w, struct{}{})
	})))
	mux.HandleFunc("POST /torrents/{id}/forget", e.started(e.withEntry(func(w http.ResponseWriter, r *http.Request, en *entry) {
		e.remove(en, false)
		reply(w, struct{}{})
	})))
	return mux
}

// started brings the engine up on the first request.
func (e *Engine) started(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := e.Ensure(r.Context()); err != nil {
			fail(w, http.StatusServiceUnavailable, err)
			return
		}
		h(w, r)
	}
}

func (e *Engine) withEntry(h func(http.ResponseWriter, *http.Request, *entry)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		en, err := e.entryByID(id)
		if err != nil {
			fail(w, http.StatusNotFound, err)
			return
		}
		h(w, r, en)
	}
}

func (e *Engine) handleList(w http.ResponseWriter, r *http.Request) {
	type item struct {
		ID           int    `json:"id"`
		InfoHash     string `json:"info_hash"`
		Name         string `json:"name"`
		OutputFolder string `json:"output_folder"`
	}
	e.mu.Lock()
	list := make([]item, 0, len(e.byID))
	for _, en := range e.byID {
		list = append(list, item{en.id, en.t.InfoHash().HexString(), en.t.Name(), outputFolder(e.opts.CacheDir, en.t)})
	}
	e.mu.Unlock()
	reply(w, map[string]any{"torrents": list})
}

func (e *Engine) handleAdd(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	magnet := string(body)
	q := r.URL.Query()
	if q.Get("list_only") == "true" {
		d, err := e.inspect(r.Context(), magnet)
		if err != nil {
			fail(w, http.StatusBadGateway, err)
			return
		}
		reply(w, torrent.Torrent{Details: d})
		return
	}
	en, err := e.add(r.Context(), magnet, q.Get("only_files_regex"))
	if err != nil {
		fail(w, http.StatusBadGateway, err)
		return
	}
	reply(w, torrent.Torrent{ID: en.id, Details: e.detail(en)})
}

// handleStream serves a file with Range support. The reader's position is
// what the engine fetches first, so a seek moves the download with it.
func (e *Engine) handleStream(w http.ResponseWriter, r *http.Request, en *entry) {
	if en.t.Info() == nil {
		fail(w, http.StatusConflict, errors.New("invalid state: initializing"))
		return
	}
	idx, err := strconv.Atoi(r.PathValue("file"))
	files := en.t.Files()
	if err != nil || idx < 0 || idx >= len(files) {
		fail(w, http.StatusNotFound, errors.New("no such file"))
		return
	}
	f := files[idx]
	reader := f.NewReader()
	defer reader.Close()
	reader.SetContext(r.Context())
	reader.SetReadahead(streamReadahead)
	reader.SetResponsive()

	name := filepath.Base(fileName(en.t, f))
	// Set, so ServeContent doesn't read the head to sniff it on every seek.
	kind := mime.TypeByExtension(filepath.Ext(name))
	if kind == "" {
		kind = "application/octet-stream"
	}
	w.Header().Set("Content-Type", kind)
	http.ServeContent(w, r, name, time.Time{}, reader)
}

func reply(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, err error) {
	http.Error(w, err.Error(), status)
}
