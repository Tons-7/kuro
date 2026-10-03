package server

import (
	"net/http"
	"strconv"

	"kuro/internal/anilist"
)

// Where a response's AniList data came from: live, or a saved copy and its age.
const (
	headerLive  = "X-Kuro-Live"
	headerSaved = "X-Kuro-Saved"
)

// served lets page reads fall back to saved answers and says so in a header.
func served(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			next.ServeHTTP(w, r)
			return
		}
		ctx, rec := anilist.WithServed(r.Context())
		next.ServeHTTP(&servedWriter{ResponseWriter: w, rec: rec}, r.WithContext(ctx))
	})
}

type servedWriter struct {
	http.ResponseWriter
	rec   *anilist.Served
	wrote bool
}

func (w *servedWriter) WriteHeader(status int) {
	if !w.wrote {
		w.wrote = true
		switch live, saved := w.rec.Result(); {
		case !saved.IsZero():
			w.Header().Set(headerSaved, strconv.FormatInt(saved.Unix(), 10))
		case live:
			w.Header().Set(headerLive, "1")
		}
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *servedWriter) Write(b []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

func (w *servedWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *servedWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
