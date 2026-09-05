package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"kuro/internal/player"
	"kuro/internal/store"
)

func decode(w http.ResponseWriter, r *http.Request, out any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(out); err != nil {
		send(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		return false
	}
	return true
}

// Must match the episode catalogue's key, which is the bare number.
func epKey(episode int) string { return fmt.Sprint(episode) }

// progress is what the browser player reports as it plays; without it nothing
// watched in a browser would ever resume or reach a tracker.
func (s *Server) progress(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AnimeID  int     `json:"animeId"`
		Episode  int     `json:"episode"`
		Position float64 `json:"position"`
		Duration float64 `json:"duration"`
		Played   float64 `json:"played"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.AnimeID == 0 || body.Episode <= 0 {
		send(w, http.StatusBadRequest, map[string]any{"error": "animeId and episode are required"})
		return
	}

	ctx := r.Context()
	// The store decides what counts as watched, for this and the mpv path
	// alike, so a client cannot claim it.
	watched, err := s.store.SavePlayback(ctx, store.PlaybackState{
		AnimeID:  body.AnimeID,
		EpKey:    epKey(body.Episode),
		Position: body.Position,
		Duration: body.Duration,
		Played:   body.Played,
	})
	if err != nil {
		s.fail(w, "save playback", err)
		return
	}

	if watched && s.syncer != nil {
		if err := s.syncer.Watched(ctx, body.AnimeID, body.Episode); err != nil {
			s.log.Warn("sync watched", "anime", body.AnimeID, "err", err)
		}
	}

	send(w, http.StatusOK, map[string]any{"saved": true, "watched": watched})
}

// prepare resolves an episode's release before anything asks to play it, so the
// wait people notice is the search rather than the streaming.
func (s *Server) prepare(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AnimeID int `json:"animeId"`
		Episode int `json:"episode"`
		Season  int `json:"season"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.AnimeID == 0 || body.Episode <= 0 {
		send(w, http.StatusBadRequest, map[string]any{"error": "animeId and episode are required"})
		return
	}
	if s.prefetch == nil {
		send(w, http.StatusOK, map[string]any{"preparing": false})
		return
	}
	// Already on screen: playback is resolving this very episode.
	if s.streams != nil {
		if _, live := s.streams.Get(sessionID(body.AnimeID, body.Episode)); live {
			send(w, http.StatusOK, map[string]any{"preparing": false})
			return
		}
	}

	// Season 0 lets the finder fill from the show's own title; forcing 1 here
	// searched a second season's episodes as if they were season one.
	s.prefetch.PrepareTarget(body.AnimeID, body.Episode, body.Season, s.preferences(r.Context()))
	send(w, http.StatusAccepted, map[string]any{"preparing": true})
}

// skips serves the opening and ending timestamps to the browser player, from
// the file's own chapters where it has them and AniSkip otherwise. The mpv path
// resolves its own in library.Playback.
func (s *Server) skips(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	animeID, _ := strconv.Atoi(q.Get("anime"))
	episode, _ := strconv.Atoi(q.Get("episode"))
	if animeID == 0 || episode <= 0 {
		send(w, http.StatusBadRequest, map[string]any{"error": "anime and episode are required"})
		return
	}
	duration, _ := strconv.Atoi(q.Get("duration"))

	ctx := r.Context()
	prefs, _ := s.store.Prefs(ctx, animeID)

	// Chapters were cut for this exact file, so they win; a file marking only
	// one of the two still needs the other fetched.
	chapters := s.chapterSkips(animeID, episode)
	source := "aniskip"
	switch {
	case len(chapters) == 0:
	case hasKind(chapters, "op") && hasKind(chapters, "ed"):
		source = "chapters"
	default:
		source = "mixed"
	}

	if source != "chapters" && s.enricher != nil {
		if err := s.enricher.Skips(ctx, animeID, episode, duration); err != nil {
			s.log.Debug("fetch skip times", "anime", animeID, "err", err)
		}
	}

	ranges := chapters
	if source != "chapters" {
		stored, err := s.store.SkipRanges(ctx, animeID, episode)
		if err != nil {
			s.fail(w, "skip ranges", err)
			return
		}
		for _, r := range stored {
			if !hasKind(ranges, r.Kind) {
				ranges = append(ranges, r)
			}
		}
	}

	send(w, http.StatusOK, map[string]any{
		"ranges":     ranges,
		"source":     source,
		"autoSkipOp": prefs.Bool("playback.autoskip_op"),
		"autoSkipEd": prefs.Bool("playback.autoskip_ed"),
	})
}

func hasKind(ranges []player.SkipRange, kind string) bool {
	for _, r := range ranges {
		if r.Kind == kind {
			return true
		}
	}
	return false
}

// chapterSkips reads the opening and ending straight off the playing file.
// Only an open session has them, which is the case whenever the browser is
// asking; nothing is stored, since another release marks its own boundaries.
func (s *Server) chapterSkips(animeID, episode int) []player.SkipRange {
	if s.streams == nil {
		return nil
	}
	session, ok := s.streams.Get(sessionID(animeID, episode))
	if !ok || session.Info == nil {
		return nil
	}

	var out []player.SkipRange
	for _, c := range session.Info.Chapters {
		if c.Kind == "" {
			continue
		}
		out = append(out, player.SkipRange{Kind: c.Kind, Start: c.Start, End: c.End})
	}
	return out
}

// setWatched marks an episode watched or not by hand. Unwatching rewinds
// progress rather than deleting it, which is what the trackers understand.
func (s *Server) setWatched(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AnimeID int   `json:"animeId"`
		Episode int   `json:"episode"`
		Watched *bool `json:"watched"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.AnimeID == 0 || body.Episode <= 0 {
		send(w, http.StatusBadRequest, map[string]any{"error": "animeId and episode are required"})
		return
	}

	ctx := r.Context()
	watched := body.Watched == nil || *body.Watched

	var err error
	switch {
	case watched && s.syncer != nil:
		err = s.syncer.Watched(ctx, body.AnimeID, body.Episode)
	case watched:
		_, err = s.store.MarkWatched(ctx, body.AnimeID, body.Episode)
	default:
		err = s.store.Unwatch(ctx, body.AnimeID, body.Episode)
	}
	if err != nil {
		s.fail(w, "set watched", err)
		return
	}
	if !watched {
		s.pushSoon(body.AnimeID)
	}
	if watched && s.cache != nil {
		if _, err := s.cache.AutoDelete(ctx, body.AnimeID); err != nil {
			s.log.Warn("auto-delete watched", "anime", body.AnimeID, "err", err)
		}
	}
	send(w, http.StatusOK, map[string]any{"animeId": body.AnimeID, "episode": body.Episode, "watched": watched})
}

// pushSoon sends one anime to the trackers behind the response.
func (s *Server) pushSoon(animeID int) {
	if s.syncer == nil {
		return
	}
	go func() {
		ctx, cancel := detached(2 * time.Minute)
		defer cancel()
		if err := s.syncer.PushOne(ctx, animeID); err != nil {
			s.log.Warn("push change", "anime", animeID, "err", err)
		}
	}()
}

// setScore rates a show 0-100; 0 clears it. Both trackers score per show.
func (s *Server) setScore(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AnimeID int `json:"animeId"`
		Score   int `json:"score"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.AnimeID == 0 {
		send(w, http.StatusBadRequest, map[string]any{"error": "animeId is required"})
		return
	}
	if body.Score < 0 || body.Score > 100 {
		send(w, http.StatusBadRequest, map[string]any{"error": "score must be 0-100"})
		return
	}
	if err := s.store.SetScore(r.Context(), body.AnimeID, body.Score); err != nil {
		s.fail(w, "set score", err)
		return
	}
	s.pushSoon(body.AnimeID)
	send(w, http.StatusOK, map[string]any{"animeId": body.AnimeID, "score": body.Score})
}

// setStatus backs the list tags: watching, completed, on hold, dropped,
// planning, rewatching.
func (s *Server) setStatus(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AnimeID int    `json:"animeId"`
		Status  string `json:"status"`
		Score   *int   `json:"score"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.AnimeID == 0 {
		send(w, http.StatusBadRequest, map[string]any{"error": "animeId is required"})
		return
	}
	if !store.ValidStatus(body.Status) {
		send(w, http.StatusBadRequest, map[string]any{
			"error": "unknown status", "allowed": store.ListStatuses,
		})
		return
	}

	// Zero is a real score meaning unrated, so "leave it alone" needs its own
	// value rather than being inferred from an absent field.
	score := -1
	if body.Score != nil {
		score = *body.Score
		if score < 0 || score > 100 {
			send(w, http.StatusBadRequest, map[string]any{"error": "score must be 0-100"})
			return
		}
	}

	ctx := r.Context()
	if err := s.store.SetListStatus(ctx, body.AnimeID, body.Status, score); err != nil {
		s.fail(w, "set status", err)
		return
	}

	// Only a show being watched now subscribes to release notifications; following
	// on any status announced every episode of merely-planned shows.
	if err := s.store.SetFollow(ctx, store.Follow{AnimeID: body.AnimeID},
		body.Status == store.StatusCurrent || body.Status == store.StatusRepeating); err != nil {
		s.log.Warn("follow on status change", "anime", body.AnimeID, "err", err)
	}

	s.pushSoon(body.AnimeID)
	send(w, http.StatusOK, map[string]any{"animeId": body.AnimeID, "status": body.Status})
}

// dismissResume removes an episode from continue-watching without pretending
// it was finished.
func (s *Server) dismissResume(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AnimeID int `json:"animeId"`
		Episode int `json:"episode"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.AnimeID == 0 || body.Episode <= 0 {
		send(w, http.StatusBadRequest, map[string]any{"error": "animeId and episode are required"})
		return
	}

	if err := s.store.DismissResume(r.Context(), body.AnimeID, epKey(body.Episode)); err != nil {
		s.fail(w, "dismiss resume", err)
		return
	}
	send(w, http.StatusOK, map[string]any{"dismissed": true})
}
