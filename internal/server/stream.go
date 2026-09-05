package server

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"kuro/internal/transcode"
)

// The browser cannot play Matroska, so everything here rewrites the container
// on the fly. Subtitles and fonts are drawn client-side rather than burned in.

func (s *Server) streamOpen(w http.ResponseWriter, r *http.Request) {
	if s.streams == nil {
		send(w, http.StatusServiceUnavailable, map[string]any{"error": "transcoder unavailable"})
		return
	}

	animeID, _ := strconv.Atoi(r.URL.Query().Get("id"))
	episode, _ := strconv.Atoi(r.URL.Query().Get("episode"))
	if animeID == 0 {
		send(w, http.StatusBadRequest, map[string]any{"error": "id is required"})
		return
	}

	source := r.URL.Query().Get("source")
	if source == "" {
		send(w, http.StatusBadRequest, map[string]any{
			"error": "source is required; start playback first to obtain a stream url",
		})
		return
	}

	// A library file is addressed by its stream route, which only the browser can
	// open. Swap in the path so ffmpeg reads the file rather than a URL it cannot.
	if path, ok := s.localSource(r.Context(), source); ok {
		source = path
	} else if !s.engineSource(source) {
		send(w, http.StatusBadRequest, map[string]any{"error": "unrecognised source"})
		return
	}

	id := sessionID(animeID, episode)
	session, err := s.streams.OpenProbing(r.Context(), id, source,
		s.readablePath(r.Context(), animeID, episode, source))
	if err != nil {
		s.fail(w, "open stream", err)
		return
	}

	// This is the episode being watched, so it is the one the cache must not
	// evict — and the only one the downloads list should call playing.
	if rec, found, err := s.store.TorrentForEpisode(r.Context(), animeID, strconv.Itoa(episode)); err == nil && found {
		if err := s.store.PinOnly(r.Context(), rec.InfoHash, rec.FileIndex); err != nil {
			s.log.Warn("pin playing episode", "anime", animeID, "episode", episode, "err", err)
		}
	}

	// Queued downloads share this connection. Whatever is on screen comes first.
	s.downloader.Hold(id)

	// The track follows the sub/dub choice on a fresh session and when the
	// choice changes; a reopen with the same choice keeps the track it is on so
	// its segments are not dropped mid-load. The player's menu (?track=) always
	// wins. Settled before the encoder starts, so it is not restarted.
	if want := r.URL.Query().Get("audio"); session.ApplyAudioPreference(want) {
		session.SetAudioTrack(transcode.ChooseAudio(session.Info, want))
	}
	if n, err := strconv.Atoi(r.URL.Query().Get("track")); err == nil {
		session.SetAudioTrack(n)
	}

	// Start the encoder now, at the resume point, rather than waiting to be asked:
	// the client reads the playlist first, and starting at zero would be restarted.
	go s.prestart(session, animeID, episode)

	base := "/api/stream/" + id
	body := map[string]any{
		"id":         id,
		"playlist":   base + "/playlist.m3u8",
		"duration":   session.Info.Duration,
		"plan":       session.CurrentPlan(),
		"video":      session.Info.Video,
		"audio":      session.Info.Audio,
		"audioTrack": session.AudioTrack(),
	}

	// Fonts start extracting now but do not hold up the reply; the client
	// picks them up from the fonts endpoint and re-renders the subtitles.
	tracks := s.subtitleTracks(session, base)
	body["subtitles"] = tracks
	body["fonts"] = []any{}
	if len(tracks) > 0 {
		body["fontsUrl"] = base + "/fonts"
		s.extractFontsAsync(session, base)
	}

	send(w, http.StatusOK, body)
}

func (s *Server) prestart(session *transcode.Session, animeID, episode int) {
	// A head start only: an already-open session is encoding for whoever holds
	// it, and a pass aimed at a stale resume point must never displace the
	// segment the player itself asks for next. Prestart refuses both.
	ctx, cancel := detached(30 * time.Second)
	defer cancel()

	from := s.resumeSegment(ctx, animeID, episode)
	if err := session.Prestart(from); err != nil {
		s.log.Debug("prestart encoder", "session", session.ID, "from", from, "err", err)
	}
}

// resumeSegment is the segment the resume position falls in, the first one the
// client asks for. Encoder and init both aim here so a resume needs one pass.
func (s *Server) resumeSegment(ctx context.Context, animeID, episode int) int {
	if at, err := s.store.ResumeAt(ctx, animeID, strconv.Itoa(episode)); err == nil && at > 0 {
		return int(at / transcode.SegmentSeconds)
	}
	return 0
}

func (s *Server) subtitleTracks(session *transcode.Session, base string) []transcode.Track {
	tracks := []transcode.Track{}
	for _, sub := range session.Info.Subtitles {
		if !transcode.Renderable(sub.Codec) {
			continue
		}
		tracks = append(tracks, transcode.Track{
			Index:    sub.Index,
			Language: sub.Language,
			Title:    sub.Title,
			Codec:    sub.Codec,
			Default:  sub.Default,
			Forced:   sub.Forced,
			URL:      fmt.Sprintf("%s/subtitle/%d", base, sub.Index),
		})
	}
	return tracks
}

// Extracting fonts blocked playback, so it runs in the background; the client
// collects the result and styled signs arrive a moment after the episode starts.
func (s *Server) extractFontsAsync(session *transcode.Session, base string) {
	if s.subtitles == nil {
		return
	}
	id := session.ID

	s.fontsMu.Lock()
	if _, known := s.fonts[id]; known {
		s.fontsMu.Unlock()
		return
	}
	if _, running := s.fontJobs[id]; running {
		s.fontsMu.Unlock()
		return
	}
	s.fontJobs[id] = struct{}{}
	s.fontsMu.Unlock()

	go func() {
		ctx, cancel := detached(5 * time.Minute)
		defer cancel()

		// Same reason as subtitles: attachments are read by demuxing the file,
		// which over the stream endpoint means downloading all of it.
		found, err := s.subtitles.ExtractFonts(ctx, s.readable(ctx, session), s.streamDir(id))
		if err != nil {
			s.log.Warn("extract fonts", "session", id, "err", err)
		}

		fonts := []transcode.FontFile{}
		for _, f := range found {
			f.URL = base + "/font/" + f.Name
			fonts = append(fonts, f)
		}

		s.fontsMu.Lock()
		// The session closed while this ran, so its directory and these files
		// are gone; recording them would serve paths that 404 for ever.
		if _, live := s.fontJobs[id]; live {
			s.fonts[id] = fonts
			delete(s.fontJobs, id)
		}
		s.fontsMu.Unlock()

		s.log.Debug("fonts extracted", "session", id, "count", len(fonts))
	}()
}

// Still extracting is a normal answer, not an error: the episode is playing.
func (s *Server) streamFonts(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	s.fontsMu.Lock()
	fonts, done := s.fonts[id]
	_, running := s.fontJobs[id]
	s.fontsMu.Unlock()

	if !done {
		send(w, http.StatusOK, map[string]any{
			"ready": false, "extracting": running, "fonts": []any{},
		})
		return
	}
	send(w, http.StatusOK, map[string]any{"ready": true, "fonts": fonts})
}

// engineSource accepts only a file on the torrent engine. The source reaches
// ffmpeg as -i, so without this a caller picks any URL or path it likes.
func (s *Server) engineSource(source string) bool {
	u, err := url.Parse(source)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	return strings.EqualFold(u.Host, s.cfg.Torrent.APIAddr)
}

var localRoute = regexp.MustCompile(`^/api/local/(\d+)/stream$`)

// localSource turns a library file's stream route back into its path.
func (s *Server) localSource(ctx context.Context, source string) (string, bool) {
	m := localRoute.FindStringSubmatch(source)
	if m == nil {
		return "", false
	}
	id, _ := strconv.Atoi(m[1])
	f, err := s.store.LocalFile(ctx, id)
	if err != nil || f.ID == 0 || f.Path == "" {
		return "", false
	}
	return f.Path, true
}

func (s *Server) streamDir(id string) string {
	return filepath.Join(s.cfg.CacheDir, "hls", id)
}

// readable prefers the on-disk file the engine is writing to over its HTTP
// stream, so reading a whole-file subtitle track needn't refetch every byte.
func (s *Server) readable(ctx context.Context, session *transcode.Session) string {
	animeID, episode, ok := parseSessionID(session.ID)
	if !ok {
		return session.Source
	}
	return s.readablePath(ctx, animeID, episode, session.Source)
}

func (s *Server) readablePath(ctx context.Context, animeID, episode int, fallback string) string {
	// Only a torrent stream has a holey cache file worth swapping in; a library
	// file is already whole, and its stale torrent record would mislead ffmpeg.
	if !isTorrentSource(fallback) {
		return fallback
	}
	rec, found, err := s.store.TorrentForEpisode(ctx, animeID, strconv.Itoa(episode))
	if err != nil || !found || rec.FilePath == "" {
		return fallback
	}

	path := filepath.Join(s.cfg.CacheDir, filepath.FromSlash(rec.FilePath))
	if _, err := os.Stat(path); err != nil {
		return fallback
	}

	// rqbit reserves full length up front, so an untouched torrent is a full-size
	// file of holes; only actually-fetched bytes make it worth reading.
	if s.cache != nil && s.cache.Downloaded(ctx, rec.InfoHash) == 0 {
		return fallback
	}
	return path
}

func (s *Server) streamPlaylist(w http.ResponseWriter, r *http.Request) {
	session, ok := s.stream(w, r)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-store")
	w.Write([]byte(session.Playlist()))
}

func (s *Server) streamInit(w http.ResponseWriter, r *http.Request) {
	session, ok := s.stream(w, r)
	if !ok {
		return
	}

	// The init segment is written by whichever pass is running; if none is, one
	// starts at the resume point. It must not redirect a running pass: the
	// player asks for init and its first segment together, and steering the
	// encoder from here killed the pass serving that segment.
	animeID, episode, _ := parseSessionID(session.ID)
	path, err := session.WaitInit(r.Context(), s.resumeSegment(r.Context(), animeID, episode), 60*time.Second)
	if err != nil {
		s.fail(w, "stream init", err)
		return
	}
	serveFile(w, r, path, "video/mp4")
}

func (s *Server) streamSegment(w http.ResponseWriter, r *http.Request) {
	session, ok := s.stream(w, r)
	if !ok {
		return
	}

	n, err := strconv.Atoi(strings.TrimSuffix(r.PathValue("segment"), ".mp4"))
	if err != nil || n < 0 {
		send(w, http.StatusBadRequest, map[string]any{"error": "invalid segment"})
		return
	}

	path, err := session.WaitSegment(r.Context(), n, 90*time.Second)
	if err != nil {
		s.log.Warn("segment", "session", session.ID, "n", n, "err", err)
		http.Error(w, "segment unavailable", http.StatusGatewayTimeout)
		return
	}
	serveFile(w, r, path, "video/mp4")
}

func (s *Server) streamSubtitle(w http.ResponseWriter, r *http.Request) {
	session, ok := s.stream(w, r)
	if !ok {
		return
	}
	if s.subtitles == nil {
		send(w, http.StatusServiceUnavailable, map[string]any{"error": "subtitles unavailable"})
		return
	}

	index, err := strconv.Atoi(r.PathValue("track"))
	if err != nil {
		send(w, http.StatusBadRequest, map[string]any{"error": "invalid track"})
		return
	}

	var codec string
	for _, sub := range session.Info.Subtitles {
		if sub.Index == index {
			codec = sub.Codec
		}
	}
	if codec == "" {
		http.NotFound(w, r)
		return
	}

	dir := s.streamDir(session.ID)
	local := s.readable(r.Context(), session)
	complete := s.sourceComplete(r.Context(), session)

	path, err := s.subtitles.Extract(r.Context(), local, dir, index, codec, complete)

	// The on-disk file is fast but holey while downloading, and ffmpeg stops at the
	// first gap; fall back to the slow in-order engine read only if nothing came.
	if (err != nil || s.subtitles.Cues(dir, index, codec) == 0) && local != session.Source {
		if p, retry := s.subtitles.Extract(r.Context(), session.Source, dir, index, codec, false); retry == nil {
			path, err = p, nil
		}
	}
	if err != nil {
		s.fail(w, "extract subtitle", err)
		return
	}

	// Read whole rather than by path: the extractor replaces the file as the track
	// fills, and Windows won't rename over an open one. A missing file is a bug.
	track, readErr := os.ReadFile(path)
	if readErr != nil {
		s.fail(w, "extract subtitle", fmt.Errorf("track %d: %w", index, readErr))
		return
	}
	// The player keeps asking while the track can still grow; this is how it
	// knows when to stop.
	w.Header().Set("X-Kuro-Complete", strconv.FormatBool(complete))
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(track)
}

// sourceComplete reports whether the bytes behind a session can still grow. A
// torrent is complete when the engine says so; anything else was already whole.
func (s *Server) sourceComplete(ctx context.Context, session *transcode.Session) bool {
	// A library file cannot grow, so its subtitles are complete at once. Judging a
	// now-local episode by its torrent record would poll for ever.
	if !isTorrentSource(session.Source) {
		return true
	}
	animeID, episode, ok := parseSessionID(session.ID)
	if !ok {
		return false
	}
	rec, found, err := s.store.TorrentForEpisode(ctx, animeID, strconv.Itoa(episode))
	if err != nil {
		return false
	}
	if !found {
		return true
	}
	if s.store.FileComplete(ctx, rec.InfoHash, rec.FileIndex) {
		return true
	}
	return s.cache != nil && s.cache.Finished(ctx, rec.InfoHash)
}

// streamThumbnails describes the scrub preview sheet, building it if not there
// yet. A missing sheet is the normal answer until ffmpeg has walked the episode.
func (s *Server) streamThumbnails(w http.ResponseWriter, r *http.Request) {
	session, ok := s.stream(w, r)
	if !ok {
		return
	}
	if s.thumbs == nil {
		send(w, http.StatusOK, transcode.Sheet{})
		return
	}

	dir := s.thumbDir(r.Context(), session)
	sheet, ready := s.thumbs.Read(dir, session.Info.Duration, aspectOf(session.Info))
	// Only from the whole file on disk: sampling through the engine would fetch
	// the episode, and a part-downloaded one is holes, which read as black.
	// A library file is its own readable path; a torrent's is the cache file.
	if !ready && s.sourceComplete(r.Context(), session) {
		if local := s.readable(r.Context(), session); !isTorrentSource(local) {
			s.thumbs.Generate(local, dir, session.Info.Duration, aspectOf(session.Info))
		}
	}
	send(w, http.StatusOK, sheet)
}

func (s *Server) streamThumbSheet(w http.ResponseWriter, r *http.Request) {
	session, ok := s.stream(w, r)
	if !ok {
		return
	}
	path := filepath.Join(s.thumbDir(r.Context(), session), "thumbs.jpg")
	if _, err := os.Stat(path); err != nil {
		http.NotFound(w, r)
		return
	}
	// Content-addressed by session, and a session is one episode.
	w.Header().Set("Cache-Control", "private, max-age=3600")
	serveFile(w, r, path, "image/jpeg")
}

// StreamHealth is whether the swarm keeps up with the picture.
type StreamHealth struct {
	// Mbps arriving from peers, and what sustained playback needs.
	Mbps   float64 `json:"mbps"`
	Needed float64 `json:"needed"`
	Peers  int     `json:"peers"`
	// Complete files and library files stream regardless.
	Complete bool `json:"complete"`
	Slow     bool `json:"slow"`
}

// streamHealth answers the player's "is it buffering or is it dead" question
// with numbers, so a slow swarm can be turned into a download instead.
func (s *Server) streamHealth(w http.ResponseWriter, r *http.Request) {
	session, ok := s.stream(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	health := StreamHealth{Complete: s.sourceComplete(ctx, session)}
	if health.Complete {
		send(w, http.StatusOK, health)
		return
	}

	animeID, episode, ok := parseSessionID(session.ID)
	if !ok || s.cache == nil {
		send(w, http.StatusOK, health)
		return
	}
	rec, found, err := s.store.TorrentForEpisode(ctx, animeID, strconv.Itoa(episode))
	if err != nil || !found {
		send(w, http.StatusOK, health)
		return
	}
	if session.Info != nil && session.Info.Duration > 0 && rec.TotalSize > 0 {
		health.Needed = float64(rec.TotalSize) * 8 / session.Info.Duration / 1e6
	}
	health.Mbps, health.Peers = s.cache.Swarm(ctx, rec.InfoHash)
	// Some headroom: a swarm exactly at the bitrate stalls on every burst.
	health.Slow = health.Needed > 0 && health.Mbps < health.Needed*1.2
	send(w, http.StatusOK, health)
}

// thumbDir outlives the session directory, which every launch purges: a sheet
// costs an ffmpeg pass over the whole episode, so it is keyed by the file.
func (s *Server) thumbDir(ctx context.Context, session *transcode.Session) string {
	key := ""
	if animeID, episode, ok := parseSessionID(session.ID); ok && isTorrentSource(session.Source) {
		if rec, found, err := s.store.TorrentForEpisode(ctx, animeID, strconv.Itoa(episode)); err == nil && found {
			key = fmt.Sprintf("%s-%d", rec.InfoHash, rec.FileIndex)
		}
	}
	if key == "" {
		sum := sha1.Sum([]byte(session.Source))
		key = hex.EncodeToString(sum[:8])
	}
	dir := filepath.Join(s.cfg.CacheDir, "thumbs", key)
	os.MkdirAll(dir, 0o755)
	return dir
}

// aspectOf is the video's shape, so preview tiles are not stretched. Anamorphic
// releases exist, but the stored dimensions are what the encoder scales to.
func aspectOf(info *transcode.MediaInfo) float64 {
	if info == nil || info.Video == nil || info.Video.Height == 0 {
		return 0
	}
	return float64(info.Video.Width) / float64(info.Video.Height)
}

func (s *Server) streamFont(w http.ResponseWriter, r *http.Request) {
	session, ok := s.stream(w, r)
	if !ok {
		return
	}

	// Only a bare filename is accepted; anything with a path separator would
	// let a request walk out of the font directory.
	name := r.PathValue("font")
	if name == "" || strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		http.NotFound(w, r)
		return
	}
	serveFile(w, r, filepath.Join(s.streamDir(session.ID), "fonts", name), "font/ttf")
}

func (s *Server) streamClose(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// Only the last holder stops anything: reopening returns the running session,
	// so a page that reopens then tears down its old effect must not kill playback.
	closed := s.streams != nil && s.streams.Close(id)

	if closed {
		s.downloader.Release(id)
		// Closing removes the session directory, so the extracted fonts are gone
		// and the next open has to find them again. Dropping the job marker too
		// stops an extractor still running from writing the dead paths back.
		s.fontsMu.Lock()
		delete(s.fonts, id)
		delete(s.fontJobs, id)
		s.fontsMu.Unlock()
	}
	// Not if the queue is fetching this same episode: closing the player is not
	// a reason to stop a download that was asked for separately.
	if animeID, episode, ok := parseSessionID(id); closed && ok && s.playback != nil &&
		!s.stillReading(r.Context(), animeID, episode) && !s.downloader.Downloading(id) {
		s.playback.Suspend(r.Context(), animeID, episode)
	}
	send(w, http.StatusOK, map[string]any{"closed": closed})
}

// stillReading reports that another open session is served by the same torrent,
// so closing one tab of a season pack must not stop a download others read from.
func (s *Server) stillReading(ctx context.Context, animeID, episode int) bool {
	if s.streams == nil {
		return false
	}

	rec, found, err := s.store.TorrentForEpisode(ctx, animeID, strconv.Itoa(episode))
	if err != nil || !found || rec.RqbitID == 0 {
		return false
	}

	marker := fmt.Sprintf("/torrents/%d/", rec.RqbitID)
	for _, source := range s.streams.Sources() {
		if strings.Contains(source, marker) {
			return true
		}
	}
	return false
}

// Session ids are "<anime>-<episode>". streamOpen builds them and
// parseSessionID reads them back.
// streamAudio switches the track of a running session: the player re-reads
// the playlist from where it is, and the encoder resumes there with the other
// track. Every segment already written carried the old one, so they go.
func (s *Server) streamAudio(w http.ResponseWriter, r *http.Request) {
	if s.streams == nil {
		send(w, http.StatusServiceUnavailable, map[string]any{"error": "transcoder unavailable"})
		return
	}
	session, ok := s.streams.Get(r.PathValue("id"))
	if !ok {
		send(w, http.StatusNotFound, map[string]any{"error": "no such stream"})
		return
	}
	track, err := strconv.Atoi(r.URL.Query().Get("track"))
	if err != nil || track < 0 || track >= len(session.Info.Audio) {
		send(w, http.StatusBadRequest, map[string]any{"error": "track is out of range"})
		return
	}
	changed := session.SetAudioTrack(track)
	send(w, http.StatusOK, map[string]any{"audioTrack": session.AudioTrack(), "changed": changed})
}

func sessionID(animeID, episode int) string {
	return fmt.Sprintf("%d-%d", animeID, episode)
}

// isTorrentSource tells a torrent's engine stream URL from a library file path.
func isTorrentSource(src string) bool {
	return strings.Contains(src, "/torrents/") && strings.Contains(src, "/stream/")
}

func parseSessionID(id string) (animeID, episode int, ok bool) {
	dash := strings.LastIndexByte(id, '-')
	if dash <= 0 {
		return 0, 0, false
	}
	animeID, err := strconv.Atoi(id[:dash])
	if err != nil {
		return 0, 0, false
	}
	episode, err = strconv.Atoi(id[dash+1:])
	if err != nil {
		return 0, 0, false
	}
	return animeID, episode, true
}

func (s *Server) stream(w http.ResponseWriter, r *http.Request) (*transcode.Session, bool) {
	if s.streams == nil {
		send(w, http.StatusServiceUnavailable, map[string]any{"error": "transcoder unavailable"})
		return nil, false
	}
	session, ok := s.streams.Get(r.PathValue("id"))
	if !ok {
		send(w, http.StatusNotFound, map[string]any{"error": "no such stream session"})
		return nil, false
	}
	return session, true
}

func serveFile(w http.ResponseWriter, r *http.Request, path, contentType string) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	http.ServeFile(w, r, path)
}
