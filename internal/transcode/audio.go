package transcode

import (
	"os"
	"regexp"
)

var (
	englishTag  = regexp.MustCompile(`(?i)^(en|eng|english)$|\benglish\b|\bdub\b`)
	japaneseTag = regexp.MustCompile(`(?i)^(ja|jp|jpn|japanese)$|\bjapanese\b`)
)

// ChooseAudio picks the track for a sub/dub preference: by language tag, then
// by the track's title, then whatever the file marks default. Positions index
// info.Audio, which is how ffmpeg's "0:a:N" counts too.
func ChooseAudio(info *MediaInfo, want string) int {
	if info == nil || len(info.Audio) < 2 {
		return 0
	}
	var lang *regexp.Regexp
	switch want {
	case "dub":
		lang = englishTag
	case "sub":
		lang = japaneseTag
	}
	if lang != nil {
		for i, a := range info.Audio {
			if lang.MatchString(a.Language) {
				return i
			}
		}
		for i, a := range info.Audio {
			if lang.MatchString(a.Title) {
				return i
			}
		}
	}
	for i, a := range info.Audio {
		if a.Default {
			return i
		}
	}
	return 0
}

func (s *Session) AudioTrack() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.audioTrack
}

// ApplyAudioPreference reports whether the sub/dub preference needs a track
// picked: on the first open, and again when the preference changes (the Sub/Dub
// toggle). A reopen with the same preference returns false, so the track the
// viewer is on is kept rather than reset mid-load.
func (s *Session) ApplyAudioPreference(want string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.audioPref == want && s.audioPref != "" {
		return false
	}
	if want == "" {
		want = "either"
	}
	s.audioPref = want
	return true
}

// SetAudioTrack switches the track being encoded. A running pass is stopped
// and what it wrote is dropped — every segment carries the old track — so the
// next request re-encodes from wherever the viewer is.
func (s *Session) SetAudioTrack(n int) bool {
	s.startMu.Lock()
	defer s.startMu.Unlock()

	s.mu.Lock()
	if n < 0 || n >= len(s.Info.Audio) || n == s.audioTrack || s.closed {
		s.mu.Unlock()
		return false
	}
	s.audioTrack = n
	s.Plan = planFor(s.Info, s.encoder, n)
	s.headFrom, s.headTo = -1, -1
	s.mu.Unlock()

	s.stop()
	// Every segment and the init file carry the old track, so all of them go:
	// leaving stale segments would serve the wrong audio, and a later request
	// would be answered from disk without the encoder ever rewriting init.mp4.
	for n := 0; n < s.Segments; n++ {
		os.Remove(s.SegmentPath(n))
	}
	os.Remove(s.InitPath())
	s.log.Info("audio track switched", "session", s.ID, "track", n)
	return true
}
