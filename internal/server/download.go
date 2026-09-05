package server

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// detached scopes work that outlives the request that started it: a download
// must not be cancelled because the browser tab moved on.
func detached(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

// download pulls one episode to disk ahead of watching it. The same machinery
// backs auto-download; this is the manual button.
func (s *Server) download(w http.ResponseWriter, r *http.Request) {
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
		send(w, http.StatusServiceUnavailable, map[string]any{"error": "torrent engine unavailable"})
		return
	}

	// The same queue as a whole season, so one episode never competes with a
	// season already being fetched.
	if _, err := s.store.Enqueue(r.Context(), body.AnimeID, body.Season, []int{body.Episode}); err != nil {
		s.fail(w, "queue download", err)
		return
	}
	s.downloader.Wake()

	send(w, http.StatusAccepted, map[string]any{
		"queued": true, "animeId": body.AnimeID, "episode": body.Episode,
	})
}

// downloadEpisodes queues a hand-picked set — the episode-list checkboxes —
// through the same queue as a whole season.
func (s *Server) downloadEpisodes(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AnimeID  int   `json:"animeId"`
		Season   int   `json:"season"`
		Episodes []int `json:"episodes"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.AnimeID == 0 || len(body.Episodes) == 0 {
		send(w, http.StatusBadRequest, map[string]any{"error": "animeId and episodes are required"})
		return
	}
	if s.prefetch == nil {
		send(w, http.StatusServiceUnavailable, map[string]any{"error": "torrent engine unavailable"})
		return
	}

	// Deduped so a repeated checkbox does not queue an episode twice.
	seen := map[int]bool{}
	episodes := make([]int, 0, len(body.Episodes))
	for _, e := range body.Episodes {
		if e > 0 && !seen[e] {
			seen[e] = true
			episodes = append(episodes, e)
		}
	}
	if len(episodes) == 0 {
		send(w, http.StatusBadRequest, map[string]any{"error": "no valid episodes"})
		return
	}

	added, err := s.store.Enqueue(r.Context(), body.AnimeID, body.Season, episodes)
	if err != nil {
		s.fail(w, "queue downloads", err)
		return
	}
	s.downloader.Wake()

	send(w, http.StatusAccepted, map[string]any{"queued": added, "animeId": body.AnimeID})
}

// downloadAll queues every episode the show is known to have. Episodes already
// on disk are skipped by the prefetcher, so re-running is cheap.
func (s *Server) downloadAll(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AnimeID int `json:"animeId"`
		Season  int `json:"season"`
		From    int `json:"from"`
		To      int `json:"to"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.AnimeID == 0 {
		send(w, http.StatusBadRequest, map[string]any{"error": "animeId is required"})
		return
	}
	if s.prefetch == nil {
		send(w, http.StatusServiceUnavailable, map[string]any{"error": "torrent engine unavailable"})
		return
	}

	from, to := body.From, body.To
	if from <= 0 {
		from = 1
	}
	if to <= 0 {
		count, err := s.store.EpisodeCount(r.Context(), body.AnimeID)
		if err != nil || count <= 0 {
			send(w, http.StatusPreconditionFailed, map[string]any{
				"error": "episode count unknown; give an explicit range",
			})
			return
		}
		to = count
	}
	// A slip in the range should not queue a thousand searches at Nyaa.
	if to-from >= 500 {
		send(w, http.StatusBadRequest, map[string]any{"error": "range too large"})
		return
	}

	episodes := make([]int, 0, to-from+1)
	for episode := from; episode <= to; episode++ {
		episodes = append(episodes, episode)
	}

	// Queued in one write, so the whole season appears at once instead of
	// trickling in as each one resolves. The worker takes them in order.
	added, err := s.store.Enqueue(r.Context(), body.AnimeID, body.Season, episodes)
	if err != nil {
		s.fail(w, "queue downloads", err)
		return
	}
	s.downloader.Wake()

	send(w, http.StatusAccepted, map[string]any{
		"queued": added, "animeId": body.AnimeID, "from": from, "to": to,
	})
}

// cancelDownloadAll drops a show's queued episodes and stops the one in
// flight. What is already on disk stays; deleting it is the downloads list.
func (s *Server) cancelDownloadAll(w http.ResponseWriter, r *http.Request) {
	animeID, _ := strconv.Atoi(r.PathValue("anime"))
	if animeID == 0 {
		send(w, http.StatusBadRequest, map[string]any{"error": "anime id is required"})
		return
	}

	removed, err := s.downloader.Cancel(r.Context(), animeID, r.URL.Query().Get("episode"))
	if err != nil {
		s.fail(w, "cancel downloads", err)
		return
	}
	send(w, http.StatusOK, map[string]any{"cancelled": removed})
}

// downloadQueue is what is waiting and what is being fetched right now.
func (s *Server) downloadQueue(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.QueuedDownloads(r.Context())
	if err != nil {
		s.fail(w, "download queue", err)
		return
	}

	waiting := map[int]int{}
	for _, q := range items {
		if q.State != "failed" {
			waiting[q.AnimeID]++
		}
	}
	send(w, http.StatusOK, map[string]any{"items": items, "waiting": waiting})
}

// downloads reports what the torrent engine currently holds, so the UI can
// show progress rather than a spinner that never resolves.
func (s *Server) downloads(w http.ResponseWriter, r *http.Request) {
	if s.cache == nil {
		send(w, http.StatusOK, map[string]any{"items": []any{}})
		return
	}

	items, err := s.store.DownloadStatus(r.Context())
	if err != nil {
		s.fail(w, "downloads", err)
		return
	}

	// bytes_on_disk reserves full length from the start, so percentages must come
	// from the engine or every download reads as finished.
	if live, err := s.cache.Progress(r.Context()); err != nil {
		s.log.Warn("download progress", "err", err)
	} else {
		for i := range items {
			p, ok := live[strings.ToLower(items[i].InfoHash)]
			if !ok {
				continue
			}
			items[i].OnDisk = p.Bytes
			items[i].Paused = p.Paused
			items[i].Checking = p.Checking
			items[i].Mbps = p.Mbps
			items[i].Peers = p.Peers
			if p.Total > 0 {
				items[i].TotalSize = p.Total
				items[i].Percent = float64(p.Bytes) / float64(p.Total) * 100
			}
			if p.Finished {
				items[i].Percent = 100
			}
		}
	}

	send(w, http.StatusOK, map[string]any{"items": items, "count": len(items)})
}

// removeDownload deletes one download and its data. Pinned entries belong to
// whatever is playing, so they are refused rather than silently skipped.
func (s *Server) removeDownload(w http.ResponseWriter, r *http.Request) {
	hash := strings.ToLower(r.PathValue("hash"))
	if hash == "" {
		send(w, http.StatusBadRequest, map[string]any{"error": "info hash is required"})
		return
	}
	if s.cache == nil {
		send(w, http.StatusServiceUnavailable, map[string]any{"error": "torrent engine unavailable"})
		return
	}

	freed, err := s.cache.Remove(r.Context(), hash)
	if err != nil {
		s.fail(w, "remove download", err)
		return
	}
	send(w, http.StatusOK, map[string]any{"removed": hash, "freedBytes": freed})
}

// pauseDownload stops fetching without discarding what is on disk, which is
// the difference between this and removing it.
func (s *Server) pauseDownload(w http.ResponseWriter, r *http.Request) {
	s.setDownloadRunning(w, r, false)
}

func (s *Server) resumeDownload(w http.ResponseWriter, r *http.Request) {
	s.setDownloadRunning(w, r, true)
}

func (s *Server) setDownloadRunning(w http.ResponseWriter, r *http.Request, run bool) {
	hash := strings.ToLower(r.PathValue("hash"))
	if hash == "" {
		send(w, http.StatusBadRequest, map[string]any{"error": "info hash is required"})
		return
	}
	if s.cache == nil {
		send(w, http.StatusServiceUnavailable, map[string]any{"error": "torrent engine unavailable"})
		return
	}

	var err error
	if run {
		err = s.cache.Resume(r.Context(), hash)
	} else {
		err = s.cache.Pause(r.Context(), hash)
	}
	if err != nil {
		s.fail(w, "pause download", err)
		return
	}
	send(w, http.StatusOK, map[string]any{"paused": !run})
}

// keepDownload moves a cached episode into the kept tier: outside the cache
// budget, never evicted. unkeepDownload hands it back to the cache.
func (s *Server) keepDownload(w http.ResponseWriter, r *http.Request) {
	s.setDownloadKept(w, r, true)
}

func (s *Server) unkeepDownload(w http.ResponseWriter, r *http.Request) {
	s.setDownloadKept(w, r, false)
}

func (s *Server) setDownloadKept(w http.ResponseWriter, r *http.Request, kept bool) {
	hash := strings.ToLower(r.PathValue("hash"))
	if hash == "" {
		send(w, http.StatusBadRequest, map[string]any{"error": "info hash is required"})
		return
	}
	if s.cache == nil {
		send(w, http.StatusServiceUnavailable, map[string]any{"error": "torrent engine unavailable"})
		return
	}
	if err := s.cache.Keep(r.Context(), hash, kept); err != nil {
		s.fail(w, "keep download", err)
		return
	}
	// The hash goes back with it so the page can update that row from the
	// answer rather than from a list it may already have refetched.
	send(w, http.StatusOK, map[string]any{"infoHash": hash, "kept": kept})
}

func (s *Server) downloadFiles(w http.ResponseWriter, r *http.Request) {
	hash := strings.ToLower(r.PathValue("hash"))
	files, err := s.store.DownloadFiles(r.Context(), hash)
	if err != nil {
		s.fail(w, "download files", err)
		return
	}
	send(w, http.StatusOK, map[string]any{"items": files})
}

func (s *Server) keepFile(w http.ResponseWriter, r *http.Request) { s.setFileKept(w, r, true) }

func (s *Server) unkeepFile(w http.ResponseWriter, r *http.Request) { s.setFileKept(w, r, false) }

func (s *Server) setFileKept(w http.ResponseWriter, r *http.Request, kept bool) {
	hash := strings.ToLower(r.PathValue("hash"))
	index, err := strconv.Atoi(r.PathValue("index"))
	if hash == "" || err != nil {
		send(w, http.StatusBadRequest, map[string]any{"error": "info hash and file index are required"})
		return
	}
	found, err := s.store.KeepFile(r.Context(), hash, index, kept)
	if err != nil {
		s.fail(w, "keep file", err)
		return
	}
	if !found {
		send(w, http.StatusNotFound, map[string]any{"error": "no such file in the download"})
		return
	}
	send(w, http.StatusOK, map[string]any{"infoHash": hash, "fileIndex": index, "kept": kept})
}

// clearDownloads removes everything not currently playing. "completed" keeps
// anything still downloading, which is the usual tidy-up.
func (s *Server) clearDownloads(w http.ResponseWriter, r *http.Request) {
	if s.cache == nil {
		send(w, http.StatusServiceUnavailable, map[string]any{"error": "torrent engine unavailable"})
		return
	}

	removed, freed, err := s.cache.Clear(r.Context(), r.URL.Query().Get("scope") == "completed")
	if err != nil {
		s.fail(w, "clear downloads", err)
		return
	}
	send(w, http.StatusOK, map[string]any{"removed": removed, "freedBytes": freed})
}
