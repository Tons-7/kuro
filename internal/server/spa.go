package server

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

// spa serves the built app. Anything that is not a real file falls back to
// index.html, because client-side routes exist only in the browser.
func spa(assets fs.FS) http.Handler {
	files := http.FileServer(http.FS(assets))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}

		info, err := fs.Stat(assets, name)
		if err != nil || info.IsDir() {
			// A missing asset is a broken build, not a client-side route: serving
			// index.html would hand HTML to a module worker and fail silently.
			if strings.HasPrefix(name, "assets/") {
				http.NotFound(w, r)
				return
			}
			serveIndex(w, r, assets)
			return
		}

		// assets/ names carry a content hash, so cache hard. index.html must never
		// be, or a new build keeps loading the previous bundle.
		if strings.HasPrefix(name, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		// Go does not know this extension, and a manifest served as plain text
		// is ignored, so the app cannot be installed to a home screen.
		if strings.HasSuffix(name, ".webmanifest") {
			w.Header().Set("Content-Type", "application/manifest+json")
		}
		files.ServeHTTP(w, r)
	})
}

func serveIndex(w http.ResponseWriter, r *http.Request, assets fs.FS) {
	page, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		http.Error(w, "app not built", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, "index.html", time.Time{}, strings.NewReader(string(page)))
}

// apiOnly answers when no frontend was built into the binary, so the failure
// reads as "run npm build" rather than a bare 404.
func apiOnly() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		send(w, http.StatusNotFound, map[string]any{
			"error": "no frontend in this build; run npm run build in web/",
			"path":  r.URL.Path,
		})
	})
}
