package server

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"kuro/internal/config"
	"kuro/internal/library"
	"kuro/internal/store"
	"kuro/internal/torrent"
	"kuro/internal/transcode"
)

func ffmpegBinaries(t *testing.T) (ffmpeg, ffprobe string) {
	t.Helper()
	root := os.Getenv("KURO_BIN")
	if root == "" {
		root = filepath.Join("..", "..", "bin")
	}
	suffix := ""
	if os.PathSeparator == '\\' {
		suffix = ".exe"
	}
	ffmpeg, ffprobe = filepath.Join(root, "ffmpeg"+suffix), filepath.Join(root, "ffprobe"+suffix)
	for _, p := range []string{ffmpeg, ffprobe} {
		if _, err := os.Stat(p); err != nil {
			t.Skipf("%s not present", p)
		}
	}
	return ffmpeg, ffprobe
}

const streamASS = `[Script Info]
ScriptType: v4.00+

[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, Bold
Style: Default,Arial,48,&H00FFFFFF,0

[Events]
Format: Layer, Start, End, Style, Text
Dialogue: 0,0:00:01.00,0:00:05.00,Default,the first line
Dialogue: 0,0:00:06.00,0:00:19.00,Default,the second line
`

// streamHarness is a server with a real transcoder and one twenty-second library
// episode (two audio tracks, one subtitle track); it returns the episode's source route.
func streamHarness(t *testing.T) (*harness, string) {
	t.Helper()
	ffmpeg, ffprobe := ffmpegBinaries(t)
	dir := t.TempDir()

	subs := filepath.Join(dir, "subs.ass")
	if err := os.WriteFile(subs, []byte(streamASS), 0o644); err != nil {
		t.Fatal(err)
	}
	episode := filepath.Join(dir, "Show - 01.mkv")
	out, err := exec.Command(ffmpeg, "-y", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc2=size=320x240:rate=24:duration=20",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=20",
		"-f", "lavfi", "-i", "sine=frequency=880:sample_rate=48000:duration=20",
		"-i", subs,
		"-map", "0:v", "-map", "1:a", "-map", "2:a", "-map", "3:s",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-g", "48",
		"-c:a", "aac", "-c:s", "ass",
		"-metadata:s:a:0", "language=jpn", "-metadata:s:a:1", "language=eng",
		"-metadata:s:s:0", "language=eng", episode).CombinedOutput()
	if err != nil {
		t.Fatalf("make the episode: %v\n%s", err, out)
	}

	cache := filepath.Join(dir, "cache")
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := newHarness(t, config.Config{CacheDir: cache}, nil)
	h.server.streams = transcode.NewManager(ffmpeg, ffprobe, cache, "libx264", log)
	h.server.subtitles = transcode.NewSubtitles(ffmpeg)
	t.Cleanup(func() { h.server.streams.Close(sessionID(1, 1)) })

	ctx := context.Background()
	episodes := 12
	if _, err := h.store.ImportList(ctx,
		[]store.Anime{{ID: 1, Romaji: "Show", Synonyms: "[]", Genres: "[]", Episodes: &episodes}},
		nil, store.ImportMerge); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(episode)
	if _, err := h.store.SaveLocalFiles(ctx, 1, []store.LocalFile{
		{Path: episode, Size: info.Size(), AnimeID: 1, Episode: 1, Season: 1, Confidence: 1},
	}); err != nil {
		t.Fatal(err)
	}
	files, err := h.store.LocalFiles(ctx, 1, false, store.Paging{Page: 1, PerPage: 10})
	if err != nil || len(files.Items) != 1 {
		t.Fatalf("library files: %+v, %v", files.Items, err)
	}
	return h, fmt.Sprintf("/api/local/%d/stream", files.Items[0].ID)
}

func (h *harness) fetch(t *testing.T, method, target string) (int, http.Header, []byte) {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	req.RemoteAddr = "127.0.0.1:12345"
	req.Host = "127.0.0.1:4321"
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)
	return rec.Code, rec.Header(), rec.Body.Bytes()
}

// The whole path a browser takes to play an episode: open, playlist, init, a segment, subtitles, close.
func TestAnEpisodeStreamsFromOpenToClose(t *testing.T) {
	h, source := streamHarness(t)

	code, opened := h.call(t, "POST", "/api/stream/open?id=1&episode=1&source="+source, "")
	if code != http.StatusOK {
		t.Fatalf("open answered %d: %v", code, opened)
	}
	if d, _ := opened["duration"].(float64); d < 19 || d > 21 {
		t.Errorf("duration %v, want about 20", opened["duration"])
	}
	if tracks, _ := opened["audio"].([]any); len(tracks) != 2 {
		t.Errorf("%d audio tracks reported, want 2", len(tracks))
	}
	subs, _ := opened["subtitles"].([]any)
	if len(subs) != 1 {
		t.Fatalf("%d subtitle tracks reported, want 1", len(subs))
	}
	base := "/api/stream/" + opened["id"].(string)
	if opened["playlist"] != base+"/playlist.m3u8" {
		t.Errorf("playlist is %v", opened["playlist"])
	}

	code, _, playlist := h.fetch(t, "GET", base+"/playlist.m3u8")
	if code != http.StatusOK || !bytes.HasPrefix(playlist, []byte("#EXTM3U")) {
		t.Fatalf("playlist answered %d: %.80s", code, playlist)
	}
	var first string
	for line := range strings.SplitSeq(string(playlist), "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			first = line
			break
		}
	}
	if first == "" {
		t.Fatalf("the playlist names no segment:\n%s", playlist)
	}

	code, _, init := h.fetch(t, "GET", base+"/init.mp4")
	if code != http.StatusOK || !bytes.Contains(init[:min(len(init), 64)], []byte("ftyp")) {
		t.Fatalf("init answered %d with %d bytes that are not an mp4 header", code, len(init))
	}
	code, _, segment := h.fetch(t, "GET", base+"/"+first)
	if code != http.StatusOK || !bytes.Contains(segment[:min(len(segment), 256)], []byte("moof")) {
		t.Fatalf("segment %s answered %d with %d bytes that are not a fragment", first, code, len(segment))
	}

	// A library file is whole, so its subtitles arrive complete and the player stops asking.
	code, header, track := h.fetch(t, "GET", subs[0].(map[string]any)["url"].(string))
	if code != http.StatusOK || !bytes.Contains(track, []byte("the first line")) || !bytes.Contains(track, []byte("the second line")) {
		t.Fatalf("subtitles answered %d: %.200s", code, track)
	}
	if header.Get("X-Kuro-Complete") != "true" {
		t.Error("a library file's subtitles were not marked complete")
	}

	if code, _, _ := h.fetch(t, "GET", base+"/health"); code != http.StatusOK {
		t.Errorf("health answered %d", code)
	}

	if code, body := h.call(t, "DELETE", base, ""); code != http.StatusOK || body["closed"] != true {
		t.Fatalf("close answered %d %v", code, body)
	}
	if code, _, _ := h.fetch(t, "GET", base+"/playlist.m3u8"); code != http.StatusNotFound {
		t.Errorf("a closed session's playlist answered %d, want 404", code)
	}
}

// An episode already on disk has nothing to give back, so the queue keeps downloading while it plays.
func TestWatchingAnEpisodeOnDiskDoesNotHoldTheQueue(t *testing.T) {
	h, source := streamHarness(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h.server.downloader = library.NewDownloader(h.store, library.NewPrefetcher(h.store, nil, nil, log), nil, log)

	if code, body := h.call(t, "POST", "/api/stream/open?id=1&episode=1&source="+source, ""); code != http.StatusOK {
		t.Fatalf("open answered %d: %v", code, body)
	}
	if h.server.downloader.Held() {
		t.Fatal("the queue stood down for an episode that is whole on disk")
	}
}

// The dialogue lift is asked for on open and switched mid-stream; either way the next segment carries it.
func TestVoiceBoostOnOpenAndMidStream(t *testing.T) {
	h, source := streamHarness(t)

	code, opened := h.call(t, "POST", "/api/stream/open?id=1&episode=1&source="+source+"&voice=medium&night=true", "")
	snd, _ := opened["sound"].(map[string]any)
	if code != http.StatusOK || snd["voice"] != "medium" || snd["night"] != true || snd["mono"] != false {
		t.Fatalf("open answered %d: %v", code, opened)
	}
	if plan, _ := opened["plan"].(map[string]any); plan["audioCopy"] != false {
		t.Errorf("plan = %v, want an audio encode with the lift", plan)
	}
	base := "/api/stream/" + opened["id"].(string)

	// One parameter at a time changes that one and keeps the rest.
	code, body := h.call(t, "POST", base+"/sound?voice=high", "")
	snd, _ = body["sound"].(map[string]any)
	if code != http.StatusOK || snd["voice"] != "high" || snd["night"] != true || body["changed"] != true {
		t.Fatalf("switch answered %d %v", code, body)
	}
	if _, body := h.call(t, "POST", base+"/sound?voice=high", ""); body["changed"] != false {
		t.Errorf("the same level again was a change: %v", body)
	}
	if code, _ := h.call(t, "POST", base+"/sound?voice=loud", ""); code != http.StatusBadRequest {
		t.Errorf("an unknown level answered %d, want 400", code)
	}
	if code, _, _ := h.fetch(t, "GET", base+"/init.mp4"); code != http.StatusOK {
		t.Errorf("init after the switch answered %d", code)
	}
	_, body = h.call(t, "POST", base+"/sound?voice=off&night=false&mono=true&boost=high", "")
	snd, _ = body["sound"].(map[string]any)
	if snd["voice"] != "" || snd["night"] != false || snd["mono"] != true || snd["boost"] != "high" {
		t.Errorf("off+mono+boost answered %v", body)
	}
	if code, _ := h.call(t, "POST", base+"/sound?boost=max", ""); code != http.StatusBadRequest {
		t.Errorf("an unknown boost answered %d, want 400", code)
	}
}

func TestStreamRefusesSegmentsThatCannotExist(t *testing.T) {
	h, source := streamHarness(t)
	if code, body := h.call(t, "POST", "/api/stream/open?id=1&episode=1&source="+source, ""); code != http.StatusOK {
		t.Fatalf("open answered %d: %v", code, body)
	}
	base := "/api/stream/" + sessionID(1, 1)

	// Past the end would start an encoder with nothing to encode.
	if code, _, _ := h.fetch(t, "GET", base+"/9999.mp4"); code != http.StatusNotFound {
		t.Errorf("a segment past the end answered %d, want 404", code)
	}
	if code, _, _ := h.fetch(t, "GET", base+"/nonsense.mp4"); code != http.StatusBadRequest {
		t.Errorf("a malformed segment answered %d, want 400", code)
	}
	if code, _, _ := h.fetch(t, "GET", base+"/subtitle/99"); code != http.StatusNotFound {
		t.Errorf("a subtitle track that does not exist answered %d, want 404", code)
	}
}

func TestStreamOpenRefusesWhatItCannotPlay(t *testing.T) {
	h, source := streamHarness(t)
	for target, want := range map[string]int{
		"/api/stream/open?episode=1&source=" + source:                  http.StatusBadRequest,
		"/api/stream/open?id=1&episode=1":                              http.StatusBadRequest,
		"/api/stream/open?id=1&episode=1&source=/api/local/999/stream": http.StatusBadRequest,
		"/api/stream/open?id=1&episode=1&source=C:/Windows/win.ini":    http.StatusBadRequest,
		"/api/stream/open?id=1&episode=1&source=http://example.com/a":  http.StatusBadRequest,
	} {
		if code, body := h.call(t, "POST", target, ""); code != want {
			t.Errorf("%s answered %d %v, want %d", target, code, body, want)
		}
	}
	if code, _, _ := h.fetch(t, "GET", "/api/stream/1-1/playlist.m3u8"); code != http.StatusNotFound {
		t.Errorf("a session never opened answered %d, want 404", code)
	}

	// No transcoder at all is said plainly rather than failing inside ffmpeg.
	bare := newHarness(t, config.Config{}, nil)
	if code, _ := bare.call(t, "POST", "/api/stream/open?id=1&episode=1&source="+source, ""); code != http.StatusServiceUnavailable {
		t.Errorf("open without a transcoder answered %d, want 503", code)
	}
}

// A library file plays without the network: the whole bar is loaded from the first look.
func TestLoadedCoversAWholeLibraryFile(t *testing.T) {
	h, source := streamHarness(t)
	code, opened := h.call(t, "POST", "/api/stream/open?id=1&episode=1&source="+source, "")
	if code != http.StatusOK {
		t.Fatalf("open answered %d: %v", code, opened)
	}
	code, body := h.call(t, "GET", "/api/stream/"+opened["id"].(string)+"/loaded", "")
	if code != http.StatusOK || body["complete"] != true {
		t.Fatalf("loaded answered %d %v", code, body)
	}
	ranges, _ := body["ranges"].([]any)
	if len(ranges) != 1 {
		t.Fatalf("ranges = %v, want one stretch", body["ranges"])
	}
	stretch, _ := ranges[0].([]any)
	if stretch[0] != float64(0) || stretch[1].(float64) < 19 {
		t.Errorf("stretch = %v, want the whole twenty seconds", stretch)
	}
}

// Downloaded bytes map onto the episode's time as if spread evenly.
func TestLoadedSecondsSpreadBytesOverTheEpisode(t *testing.T) {
	got := loadedSeconds(torrent.FileRanges{Length: 1000, Ranges: []torrent.ByteRange{{From: 0, To: 250}, {From: 500, To: 1000}}}, 100)
	if len(got) != 2 || got[0] != [2]float64{0, 25} || got[1] != [2]float64{50, 100} {
		t.Errorf("got %v", got)
	}
	if n := len(loadedSeconds(torrent.FileRanges{}, 100)); n != 0 {
		t.Errorf("no length, yet %d stretches", n)
	}
}
