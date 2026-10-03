package player

import (
	"strings"
	"testing"
)

func TestAudioLanguagesForMpv(t *testing.T) {
	if !strings.Contains(AudioLanguages("dub"), "eng") {
		t.Error("dub should prefer english")
	}
	if !strings.Contains(AudioLanguages("sub"), "jpn") {
		t.Error("sub should prefer japanese")
	}
	for _, pref := range []string{"either", ""} {
		if AudioLanguages(pref) != "" {
			t.Errorf("%q should impose no language", pref)
		}
	}
}

// Files tag tracks "eng" or "english" as often as "en"; the order is kept.
func TestSubtitleLanguagesForMpv(t *testing.T) {
	if got := SubtitleLanguages([]string{"en", "pt"}); got != "en,eng,english,pt,por,portuguese,pt-br" {
		t.Errorf("slang = %q", got)
	}
	if got := SubtitleLanguages([]string{"xx"}); got != "xx" {
		t.Errorf("an unknown code should pass through, got %q", got)
	}
	if got := SubtitleLanguages(nil); got != "" {
		t.Errorf("no languages should impose none, got %q", got)
	}
}
