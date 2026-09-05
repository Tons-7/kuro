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
