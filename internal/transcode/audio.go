package transcode

import (
	"regexp"
)

var (
	englishTag  = regexp.MustCompile(`(?i)^(en|eng|english)$|\benglish\b|\bdub\b`)
	japaneseTag = regexp.MustCompile(`(?i)^(ja|jp|jpn|japanese)$|\bjapanese\b`)
)

// ChooseAudio picks by language tag, then title, then the file's default; an index into info.Audio (0:a:N).
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

// ApplyAudioPreference: pick a track on first open or a changed preference, not on a same-choice reopen.
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

// SetAudioTrack switches the encoded track, stopping the pass and dropping segments with the old one.
func (s *Session) SetAudioTrack(n int) bool {
	s.startMu.Lock()
	defer s.startMu.Unlock()

	s.mu.Lock()
	if n < 0 || n >= len(s.Info.Audio) || n == s.audioTrack || s.closed {
		s.mu.Unlock()
		return false
	}
	s.audioTrack = n
	s.Plan = planFor(s.Info, s.encoder, n, s.sound)
	s.headFrom, s.headTo = -1, -1
	s.mu.Unlock()

	s.stop()
	// Every segment and the init file carry the old track, so all of them go.
	s.wipeOutput()
	s.log.Info("audio track switched", "session", s.ID, "track", n)
	return true
}
