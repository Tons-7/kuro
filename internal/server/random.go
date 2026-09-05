package server

import (
	"net/http"
	"strconv"

	"kuro/internal/metadata"
	"kuro/internal/store"
)

// random backs the "surprise me" button. Ids come from the corpus for a uniform
// draw that includes titles AniList lacks; genres are AniList's, so a draw
// filtered by them goes there.
func (s *Server) random(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	year, _ := strconv.Atoi(q.Get("year"))
	format := q.Get("format")
	genres := csv(q.Get("genres"))

	// A few candidates cover ids that no longer resolve upstream. An empty
	// corpus (a fresh install still seeding) falls through to AniList.
	if len(genres) == 0 {
		ids, err := s.store.RandomIDs(r.Context(), format, year, 5)
		if err != nil {
			s.fail(w, "random", err)
			return
		}
		for _, id := range ids {
			if body, ok := s.animeCard(r, id); ok {
				send(w, http.StatusOK, body)
				return
			}
		}
	}

	m, err := s.anilist.Random(r.Context(), format, genres, year)
	if err != nil {
		s.fail(w, "random", err)
		return
	}
	if body, ok := s.animeCard(r, m.ID); ok {
		send(w, http.StatusOK, body)
		return
	}
	send(w, http.StatusNotFound, map[string]any{"error": "no anime matched"})
}

// characters serves a show's cast. Kept off the detail payload: it is a second
// upstream call, and the page is useful long before the cast arrives.
func (s *Server) characters(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || id == 0 {
		send(w, http.StatusBadRequest, map[string]any{"error": "bad anime id"})
		return
	}
	if s.enricher == nil {
		send(w, http.StatusOK, map[string]any{"items": []any{}})
		return
	}

	cast, err := s.enricher.Characters(r.Context(), id)
	if err != nil {
		// A show the catalogue never mapped to MyAnimeList has no cast to find,
		// which is not worth failing the page over.
		s.log.Warn("characters", "anime", id, "err", err)
		send(w, http.StatusOK, map[string]any{"items": []any{}})
		return
	}
	send(w, http.StatusOK, map[string]any{"items": cast, "count": len(cast)})
}

// extra serves a show's themes, key staff and trailer. Two upstream requests,
// so it is kept off the detail payload the same way the cast is.
func (s *Server) extra(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || id == 0 {
		send(w, http.StatusBadRequest, map[string]any{"error": "bad anime id"})
		return
	}
	if s.enricher == nil {
		send(w, http.StatusOK, metadata.Extra{})
		return
	}

	out, err := s.enricher.Extra(r.Context(), id)
	if err != nil {
		// A show the catalogue never mapped to MyAnimeList has none of this,
		// which is not worth failing the page over.
		s.log.Warn("anime extra", "anime", id, "err", err)
		send(w, http.StatusOK, metadata.Extra{})
		return
	}
	send(w, http.StatusOK, out)
}

// anime serves the detail page. It resolves against whichever catalogue the id
// belongs to, so a MyAnimeList-only show opens the same way an AniList one does.
func (s *Server) anime(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || id == 0 {
		send(w, http.StatusBadRequest, map[string]any{"error": "bad anime id"})
		return
	}

	body, ok := s.animeCard(r, id)
	if !ok {
		send(w, http.StatusNotFound, map[string]any{"error": "anime not found"})
		return
	}

	// Progress and list status live locally and are what the page's controls
	// bind to, so they travel with the record rather than needing a second call.
	if entry, err := s.store.ListEntry(r.Context(), id); err == nil && entry.AnimeID != 0 {
		body["listStatus"] = entry.Status
		body["progress"] = entry.Progress
		body["episodeCount"] = entry.Episodes
		body["repeat"] = entry.Repeat
		body["score"] = entry.Score
	}
	if b, err := s.store.Bookmark(r.Context(), id); err == nil {
		body["bookmark"] = b
	}

	// An episode left part way is what "continue" should mean, ahead of the
	// tracker's count — which a jump to the end can have advanced already.
	if p, ok, err := s.store.LastInProgress(r.Context(), id); err == nil && ok {
		if episode, err := strconv.Atoi(p.EpKey); err == nil {
			body["resume"] = map[string]any{
				"episode": episode, "position": p.Position, "duration": p.Duration,
			}
		}
	}
	send(w, http.StatusOK, body)
}

// animeCard resolves one corpus id against whichever catalogue it came from.
func (s *Server) animeCard(r *http.Request, id int) (map[string]any, bool) {
	titles := s.store.TitleMode(r.Context(), 0)

	onList, err := s.store.ListProgress(r.Context())
	if err != nil {
		s.log.Warn("list progress", "err", err)
	}
	progress, listed := onList[id]

	if malID := store.MALIDOf(id); malID > 0 {
		detail, err := s.enricher.Anime(r.Context(), malID)
		if err == nil {
			var english *string
			if detail.English != "" {
				english = &detail.English
			}
			return map[string]any{
				"id":       id,
				"malId":    malID,
				"source":   "myanimelist",
				"anime":    detail,
				"title":    store.PickTitle(titles, detail.Romaji, english),
				"onList":   listed,
				"progress": progress,
				"sync":     syncable(false, true),
			}, true
		}

		// Jikan is unreliable for exactly the MAL-only titles; dropping them would
		// bias the random draw toward AniList, so the corpus record stands in.
		s.log.Warn("jikan lookup failed, using corpus record", "mal", malID, "err", err)

		rec, recErr := s.store.CorpusRecord(r.Context(), id)
		if recErr != nil || rec.AnimeID == 0 {
			return nil, false
		}
		return map[string]any{
			"id":       id,
			"malId":    malID,
			"source":   "corpus",
			"anime":    rec,
			"title":    rec.Primary,
			"partial":  true,
			"onList":   listed,
			"progress": progress,
			"sync":     syncable(false, true),
		}, true
	}

	media, err := s.anilist.MediaByIDs(r.Context(), []int{id})
	if err != nil || len(media) == 0 {
		s.log.Warn("anime lookup failed", "anime", id, "returned", len(media), "err", err)
		return nil, false
	}
	m := media[0]

	// Keep the local record current: facts like the adult flag decide which
	// trackers are searched, and a stub row created elsewhere never had them.
	if s.importer != nil {
		if _, err := s.importer.Save(r.Context(), media); err != nil {
			s.log.Warn("save anime", "anime", id, "err", err)
		}
	}

	var romaji string
	if m.Title.Romaji != nil {
		romaji = *m.Title.Romaji
	}
	body := map[string]any{
		"id":       id,
		"source":   "anilist",
		"anime":    m,
		"title":    store.PickTitle(titles, romaji, m.Title.English),
		"onList":   listed,
		"progress": progress,
		"sync":     syncable(true, m.IDMal != nil),
	}
	if m.IDMal != nil {
		body["malId"] = *m.IDMal
	}
	return body, true
}

// syncable reports which trackers can hold a list entry for this show. A MAL-only
// title has no AniList entry, and many AniList entries lack a MAL id, so sync is one-way.
func syncable(anilist, mal bool) map[string]bool {
	return map[string]bool{"anilist": anilist, "mal": mal}
}
