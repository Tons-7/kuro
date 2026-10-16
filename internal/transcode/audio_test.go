package transcode

import (
	"kuro/internal/sound"
	"strings"
	"testing"
)

func dualAudioInfo() *MediaInfo {
	return &MediaInfo{
		Video: &Stream{Codec: "h264", BitDepth: 8},
		Audio: []Stream{
			{Codec: "aac", Language: "jpn", Title: "Japanese", Default: true},
			{Codec: "aac", Language: "eng", Title: "English Dub"},
		},
	}
}

// Dub gets English even with Japanese marked default; sub gets Japanese.
func TestChooseAudioByPreference(t *testing.T) {
	info := dualAudioInfo()
	if got := ChooseAudio(info, "dub"); got != 1 {
		t.Errorf("dub picked track %d, want 1", got)
	}
	if got := ChooseAudio(info, "sub"); got != 0 {
		t.Errorf("sub picked track %d, want 0", got)
	}
	if got := ChooseAudio(info, "either"); got != 0 {
		t.Errorf("either picked track %d, want the default 0", got)
	}

	// Title carries the language when the tag does not.
	byTitle := &MediaInfo{Audio: []Stream{
		{Codec: "aac", Title: "Japanese 2.0"},
		{Codec: "aac", Title: "English 5.1"},
	}}
	if got := ChooseAudio(byTitle, "dub"); got != 1 {
		t.Errorf("dub by title picked %d, want 1", got)
	}

	// A dub with no English track at all falls back to the default rather than
	// guessing; the release filter is what keeps a sub-only file out.
	subOnly := &MediaInfo{Audio: []Stream{
		{Codec: "aac", Language: "jpn"}, {Codec: "aac", Language: "jpn", Default: true},
	}}
	if got := ChooseAudio(subOnly, "dub"); got != 1 {
		t.Errorf("dub with no English picked %d, want the default 1", got)
	}

	// A single-track file has nothing to choose.
	if got := ChooseAudio(&MediaInfo{Audio: []Stream{{Codec: "aac"}}}, "dub"); got != 0 {
		t.Errorf("single track picked %d, want 0", got)
	}
}

// The preference picks a track on first open and on change, never on a same-choice reopen.
func TestAudioPreferenceAppliesOnFirstOpenAndOnChange(t *testing.T) {
	s := &Session{Info: dualAudioInfo()}
	if !s.ApplyAudioPreference("sub") {
		t.Fatal("the first open should pick a track")
	}
	if s.ApplyAudioPreference("sub") {
		t.Error("a reopen with the same preference must keep the current track")
	}
	if !s.ApplyAudioPreference("dub") {
		t.Error("the Sub/Dub toggle must re-pick")
	}
	if s.ApplyAudioPreference("dub") {
		t.Error("the same preference again is a no-op")
	}
}

// The chosen track is the one ffmpeg maps and the one the plan judges.
func TestAudioTrackDrivesArgsAndPlan(t *testing.T) {
	info := &MediaInfo{
		Video: &Stream{Codec: "h264", BitDepth: 8},
		Audio: []Stream{{Codec: "aac"}, {Codec: "ac3"}},
	}
	s := &Session{Info: info, encoder: "libx264", Plan: PlanFor(info, "libx264")}

	if !strings.Contains(strings.Join(s.args(0, 0), " "), "0:a:0?") {
		t.Error("default maps the first audio stream")
	}
	if !s.Plan.AudioCopy {
		t.Error("first track is aac, should copy")
	}

	s.audioTrack = 1
	s.Plan = planFor(info, "libx264", 1, sound.Options{})
	if !strings.Contains(strings.Join(s.args(0, 0), " "), "0:a:1?") {
		t.Error("switched track is mapped")
	}
	if s.Plan.AudioCopy {
		t.Error("second track is ac3, should re-encode")
	}
}
