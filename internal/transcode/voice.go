package transcode

import (
	"os"

	"kuro/internal/sound"
)

// Sound is what is being done to the audio on its way out; see package sound.
func (s *Session) Sound() sound.Options {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sound
}

// Voice is the dialogue lift in effect: "off", "medium" or "high".
func (s *Session) Voice() string { return s.Sound().Level() }

// SetVoice switches the dialogue lift alone.
func (s *Session) SetVoice(level string) bool {
	if !sound.ValidVoice(level) {
		return false
	}
	o := s.Sound()
	o.Voice = level
	return s.SetSound(o)
}

// SetSound switches what is done to the audio. Every segment carries the old sound, so, as with a
// track switch, all of them go and the next request re-encodes from wherever the viewer is.
func (s *Session) SetSound(o sound.Options) bool {
	o = sound.Normalized(o)
	if !sound.ValidVoice(o.Voice) || !sound.ValidBoost(o.Boost) {
		return false
	}
	s.startMu.Lock()
	defer s.startMu.Unlock()

	s.mu.Lock()
	if o == s.sound || s.closed {
		s.mu.Unlock()
		return false
	}
	s.sound = o
	s.Plan = planFor(s.Info, s.encoder, s.audioTrack, o)
	s.headFrom, s.headTo = -1, -1
	s.mu.Unlock()

	s.stop()
	for n := 0; n < s.Segments; n++ {
		os.Remove(s.SegmentPath(n))
	}
	os.Remove(s.InitPath())
	s.log.Info("sound switched", "session", s.ID, "voice", o.Level(), "night", o.Night,
		"headphones", o.Headphones, "mono", o.Mono, "boost", o.Boost)
	return true
}

// soundFilter is the ffmpeg audio filter chain in effect, "" when nothing is asked for.
func (s *Session) soundFilter() string {
	channels := 2
	if s.Info != nil && s.audioTrack < len(s.Info.Audio) && s.Info.Audio[s.audioTrack].Channels > 0 {
		channels = s.Info.Audio[s.audioTrack].Channels
	}
	return sound.Chain(s.sound, channels)
}
