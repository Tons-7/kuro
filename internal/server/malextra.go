package server

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"kuro/internal/store"
)

// MyAnimeList's own API has no favourites; Jikan reads the public profile.
var jikanBase = "https://api.jikan.moe/v4"

// malFavouritesImport pulls the favourites off the connected MAL profile, once.
// One-way by nature, so it is a button rather than part of the sync.
func (s *Server) malFavouritesImport(w http.ResponseWriter, r *http.Request) {
	name, _ := s.store.Setting(r.Context(), "mal.user_name")
	if name == "" {
		send(w, http.StatusBadRequest, map[string]any{"error": "MyAnimeList is not connected"})
		return
	}

	ids, err := jikanFavourites(r.Context(), jikanBase, name)
	if err != nil {
		s.fail(w, "mal favourites", err)
		return
	}

	var matched, favourited int
	var unmatched []int
	for _, malID := range ids {
		id, ok, err := s.store.AnimeIDByMAL(r.Context(), malID)
		if err != nil {
			s.fail(w, "mal favourites", err)
			return
		}
		if !ok {
			unmatched = append(unmatched, malID)
			continue
		}
		matched++
		held, err := s.store.Bookmark(r.Context(), id)
		if err != nil {
			s.fail(w, "mal favourites", err)
			return
		}
		if held.Favourite {
			continue
		}
		held.Favourite = true
		if err := s.store.SetBookmark(r.Context(), id, held); err != nil {
			s.fail(w, "mal favourites", err)
			return
		}
		favourited++
	}
	send(w, http.StatusOK, map[string]any{
		"found": len(ids), "matched": matched, "favourited": favourited,
		"unmatched": len(unmatched),
	})
}

func jikanFavourites(ctx context.Context, base, user string) ([]int, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		base+"/users/"+url.PathEscape(user)+"/favorites", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "kuro")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("jikan: HTTP %d", res.StatusCode)
	}

	var body struct {
		Data struct {
			Anime []struct {
				MalID int `json:"mal_id"`
			} `json:"anime"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&body); err != nil {
		return nil, err
	}
	ids := make([]int, 0, len(body.Data.Anime))
	for _, a := range body.Data.Anime {
		ids = append(ids, a.MalID)
	}
	return ids, nil
}

// malExport is the list MyAnimeList lets a user download, gzipped or not.
type malExport struct {
	Anime []struct {
		ID        int    `xml:"series_animedb_id"`
		Title     string `xml:"series_title"`
		Watched   int    `xml:"my_watched_episodes"`
		Start     string `xml:"my_start_date"`
		Finish    string `xml:"my_finish_date"`
		Score     int    `xml:"my_score"`
		Status    string `xml:"my_status"`
		Rewatched int    `xml:"my_times_watched"`
	} `xml:"anime"`
}

var malStatuses = map[string]string{
	"watching": "CURRENT", "completed": "COMPLETED", "on-hold": "PAUSED",
	"dropped": "DROPPED", "plan to watch": "PLANNING",
}

func isGzip(b []byte) bool { return len(b) > 2 && b[0] == 0x1f && b[1] == 0x8b }

func looksLikeXML(b []byte) bool {
	return bytes.HasPrefix(bytes.TrimLeft(b, "\xef\xbb\xbf \t\r\n"), []byte("<"))
}

// malEntries turns a MAL export into library entries. Ids the catalogue cannot
// place are counted, not guessed.
func (s *Server) malEntries(ctx context.Context, raw []byte) ([]store.ExportEntry, int, error) {
	if isGzip(raw) {
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, 0, err
		}
		if raw, err = io.ReadAll(io.LimitReader(zr, 64<<20)); err != nil {
			return nil, 0, err
		}
	}

	var export malExport
	if err := xml.Unmarshal(raw, &export); err != nil {
		return nil, 0, fmt.Errorf("not a MyAnimeList export: %w", err)
	}

	var entries []store.ExportEntry
	var unmatched int
	for _, a := range export.Anime {
		id, ok, err := s.store.AnimeIDByMAL(ctx, a.ID)
		if err != nil {
			return nil, 0, err
		}
		if !ok {
			unmatched++
			continue
		}
		mal := a.ID
		entries = append(entries, store.ExportEntry{
			AnimeID:     id,
			MalID:       &mal,
			Title:       a.Title,
			Status:      malStatuses[normaliseStatus(a.Status)],
			Progress:    a.Watched,
			Score:       a.Score * 10,
			Repeat:      a.Rewatched,
			StartedAt:   malDate(a.Start),
			CompletedAt: malDate(a.Finish),
		})
	}
	return entries, unmatched, nil
}

func normaliseStatus(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func malDate(s string) string {
	if strings.HasPrefix(s, "0000") {
		return ""
	}
	return s
}
