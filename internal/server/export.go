package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"kuro/internal/store"
)

// LibraryFile is what an export writes and an import reads. The version is
// there so a future change can be recognised rather than half-applied.
type LibraryFile struct {
	Kind       string              `json:"kind"`
	Version    int                 `json:"version"`
	ExportedAt string              `json:"exportedAt"`
	Entries    []store.ExportEntry `json:"entries"`
}

const (
	libraryKind    = "kuro.library"
	libraryVersion = 1
)

func (s *Server) exportLibrary(w http.ResponseWriter, r *http.Request) {
	entries, err := s.store.ExportLibrary(r.Context())
	if err != nil {
		s.fail(w, "export library", err)
		return
	}

	ext, ctype := "json", "application/json"
	if r.URL.Query().Get("format") == "txt" {
		ext, ctype = "txt", "text/plain; charset=utf-8"
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Disposition",
		`attachment; filename="kuro-library-`+time.Now().Format("2006-01-02")+`.`+ext+`"`)
	w.Header().Set("Cache-Control", "no-store")

	if ext == "txt" {
		writeLibraryText(w, entries)
		return
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(LibraryFile{
		Kind:       libraryKind,
		Version:    libraryVersion,
		ExportedAt: time.Now().UTC().Format(time.RFC3339),
		Entries:    entries,
	})
}

func writeLibraryText(w http.ResponseWriter, entries []store.ExportEntry) {
	for _, e := range entries {
		var b strings.Builder
		b.WriteString(e.Title)

		progress := fmt.Sprintf("%d", e.Progress)
		if e.Episodes != nil && *e.Episodes > 0 {
			progress = fmt.Sprintf("%d/%d", e.Progress, *e.Episodes)
		}
		fmt.Fprintf(&b, " — %s — %s", orDash(e.Status), progress)
		if e.Score > 0 {
			fmt.Fprintf(&b, " — scored %d", e.Score)
		}
		if e.Repeat > 0 {
			fmt.Fprintf(&b, " — rewatched %d", e.Repeat)
		}
		if e.Favourite {
			b.WriteString(" — favourite")
		}
		fmt.Fprintf(w, "%s\n", b.String())
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// importLibrary merges a file exported here, or a MyAnimeList export. Anything
// already watched further stays where it is; what applies is queued for the
// trackers.
func (s *Server) importLibrary(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<20))
	if err != nil {
		send(w, http.StatusBadRequest, map[string]any{"error": "could not read the file"})
		return
	}

	var file LibraryFile
	var unmatched int
	if isGzip(raw) || looksLikeXML(raw) {
		entries, missed, err := s.malEntries(r.Context(), raw)
		if err != nil {
			send(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		file.Entries, unmatched = entries, missed
	} else if err := json.Unmarshal(raw, &file); err != nil {
		send(w, http.StatusBadRequest, map[string]any{"error": "not a readable library file"})
		return
	}
	if file.Kind != "" && file.Kind != libraryKind {
		send(w, http.StatusBadRequest, map[string]any{
			"error": fmt.Sprintf("%q is not a kuro library file", file.Kind),
		})
		return
	}
	if file.Version > libraryVersion {
		send(w, http.StatusBadRequest, map[string]any{
			"error": fmt.Sprintf("this file is version %d; this kuro reads up to %d",
				file.Version, libraryVersion),
		})
		return
	}
	if len(file.Entries) == 0 {
		msg := "the file holds no entries"
		if unmatched > 0 {
			msg = fmt.Sprintf("none of the %d entries could be matched to the catalogue", unmatched)
		}
		send(w, http.StatusBadRequest, map[string]any{"error": msg})
		return
	}

	rep, err := s.store.ImportEntries(r.Context(), file.Entries)
	if err != nil {
		s.fail(w, "import library", err)
		return
	}
	rep.Skipped += unmatched

	// Titles for ids the corpus lacks arrive after the reply; a big file is
	// many rate-limited requests.
	ids := make([]int, 0, len(file.Entries))
	for _, e := range file.Entries {
		ids = append(ids, e.AnimeID)
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		s.hydrate(ctx, ids)
	}()
	send(w, http.StatusOK, rep)
}
