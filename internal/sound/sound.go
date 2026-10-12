// Package sound is what kuro does to an episode's audio on the way out (dialogue lift, night mode,
// headphones, mono, volume boost): one description, rendered for ffmpeg, mpv and VLC.
package sound

import (
	"fmt"
	"math"
	"strings"
)

// Options is the viewer's choice; the zero value changes nothing.
type Options struct {
	// Voice lifts dialogue over music and effects: "", "medium" (+6 dB) or "high" (+12 dB).
	Voice string `json:"voice"`
	// Night narrows the gap between loud effects and quiet lines.
	Night bool `json:"night"`
	// Headphones feeds a little of each channel to the other ear, as speakers in a room do.
	Headphones bool `json:"headphones"`
	// Mono plays both channels in both ears: a voice mixed to one side reaches both.
	Mono bool `json:"mono"`
	// Boost raises the whole mix, for a quiet release: "", "medium" (+6 dB) or "high" (+12 dB).
	Boost string `json:"boost"`
}

// The boost in dB per level: twice and four times as loud, limited so nothing clips.
var boostLevels = map[string]float64{"medium": 6, "high": 12}

// The lift in dB per level, after broadcast guidance (MPEG-H dialogue enhancement): up to 6 dB is
// typical, 12 dB the ceiling.
var voiceLevels = map[string]float64{"medium": 6, "high": 12}

// ValidVoice accepts off, "" and the levels; ValidBoost the same for the volume boost.
func ValidVoice(level string) bool {
	_, ok := voiceLevels[level]
	return ok || level == "" || level == "off"
}

func ValidBoost(level string) bool {
	_, ok := boostLevels[level]
	return ok || level == "" || level == "off"
}

// Normalized folds "off" into the zero value.
func Normalized(o Options) Options {
	if o.Voice == "off" {
		o.Voice = ""
	}
	if o.Boost == "off" {
		o.Boost = ""
	}
	return o
}

// Any reports whether anything is asked for.
func (o Options) Any() bool {
	o = Normalized(o)
	return o.Voice != "" || o.Night || o.Headphones || o.Mono || o.Boost != ""
}

// Level is the voice setting as shown: "off" or the level.
func (o Options) Level() string {
	if o = Normalized(o); o.Voice == "" {
		return "off"
	}
	return o.Voice
}

const (
	// Speech intelligibility lives around 1-4 kHz; a small, broad lift there, on the dialogue only.
	presence = "equalizer=f=2500:t=q:w=1:g=2"
	// A moderate compressor is what a receiver's night mode is: 3:1 above -24 dB, 6 dB made up.
	night = "acompressor=threshold=0.0631:ratio=3:attack=20:release=250:makeup=2"
	// Bauer-style crossfeed: each channel bleeds a little into the other, below 700 Hz most of all.
	headphones = "crossfeed=strength=0.3:range=0.5"
	mono       = "pan=stereo|FL=0.5*FL+0.5*FR|FR=0.5*FL+0.5*FR"
	// Latency compensated, no make-up: lifted lines must not clip, nothing else changes.
	limiter = "alimiter=limit=0.98:level=0:latency=1"
)

// Chain is the ffmpeg filter graph for a track with that many channels, "" when nothing is asked for.
// It always ends in stereo.
func Chain(o Options, channels int) string {
	o = Normalized(o)
	if !o.Any() {
		return ""
	}
	if channels <= 0 {
		channels = 2
	}
	var parts []string
	switch {
	case channels == 1:
		if o.Voice != "" {
			// Mono is all dialogue: nothing to lift it over.
			parts = append(parts, presence)
		}
		parts = append(parts, "aformat=channel_layouts=stereo")
	case channels == 2:
		if gain, ok := voiceLevels[o.Voice]; ok {
			// original=0: the sides stay as they are and only detected speech is added back, scaled so a
			// fully centred voice gains the level asked for (enhance × pan gain = lift − 1).
			lift := math.Pow(10, gain/20)
			enhance, pan := 2.0, (lift-1)/2
			if gain >= 12 {
				enhance, pan = 3, (lift-1)/3
			}
			parts = append(parts,
				fmt.Sprintf("dialoguenhance=original=0:enhance=%g:voice=2", enhance),
				presence+":c=FC",
				fmt.Sprintf("pan=stereo|FL=FL+%.3f*FC|FR=FR+%.3f*FC", pan, pan))
		}
	default:
		if gain, ok := voiceLevels[o.Voice]; ok {
			// ffmpeg's own fold-down is 0.414 front, 0.293 centre and rear; only the centre moves.
			centre := 0.293 * math.Pow(10, gain/20)
			parts = append(parts, "aformat=channel_layouts=5.1", presence+":c=FC",
				fmt.Sprintf("pan=stereo|FL=0.414*FL+%.3f*FC+0.293*BL|FR=0.414*FR+%.3f*FC+0.293*BR", centre, centre))
		} else {
			parts = append(parts, "aformat=channel_layouts=stereo")
		}
	}
	if o.Night {
		parts = append(parts, night)
	}
	if o.Headphones && !o.Mono {
		parts = append(parts, headphones)
	}
	if o.Mono {
		parts = append(parts, mono)
	}
	if gain, ok := boostLevels[o.Boost]; ok {
		parts = append(parts, fmt.Sprintf("volume=%gdB", gain))
	}
	return strings.Join(append(parts, limiter), ",")
}

// MPVArgs renders the same for mpv, which carries ffmpeg's filters: its own fold-down to stereo first,
// so one graph serves every layout, the centre of a surround mix landing where the extraction finds it.
func MPVArgs(o Options) []string {
	chain := Chain(o, 2)
	if chain == "" {
		return nil
	}
	return []string{"--audio-channels=stereo", "--af=lavfi=[" + chain + "]"}
}

// VLCArgs renders what VLC can do: its compressor, its own headphone and mono modes, and for the voice a
// lift of the speech bands of its equalizer, which is as close as it gets without an extraction.
func VLCArgs(o Options) []string {
	o = Normalized(o)
	if !o.Any() {
		return nil
	}
	var filters, args []string
	if o.Voice != "" {
		// Bands: 60 170 310 600 1k 3k 6k 12k 14k 16k Hz; no preamp, or everything gets louder.
		bands := "0 0 0 0 3 4 2 0 0 0"
		if o.Voice == "high" {
			bands = "0 0 0 0 5 7 3 0 0 0"
		}
		filters = append(filters, "equalizer")
		args = append(args, "--equalizer-preamp=0", "--equalizer-bands="+bands)
	}
	if o.Night {
		filters = append(filters, "compressor")
		args = append(args, "--compressor-threshold=-24", "--compressor-ratio=3", "--compressor-attack=20",
			"--compressor-release=250", "--compressor-makeup-gain=6")
	}
	switch {
	case o.Mono:
		args = append(args, "--stereo-mode=7")
	case o.Headphones:
		args = append(args, "--stereo-mode=6")
	}
	if gain, ok := boostLevels[o.Boost]; ok {
		args = append(args, fmt.Sprintf("--gain=%g", math.Pow(10, gain/20)))
	}
	if len(filters) > 0 {
		args = append([]string{"--audio-filter=" + strings.Join(filters, ":")}, args...)
	}
	return args
}
