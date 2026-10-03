package library

import (
	"testing"

	"kuro/internal/parse"
)

// A whole-season pack confirms an episode of its season.
func TestSeasonPackConfirmsItsOwnSeason(t *testing.T) {
	pack := parse.Parse("Monster.2004.S01.1080p.BluRay.DUAL.FLAC.2.0.x264-Kitsune")
	if !pack.Batch || pack.Season != 1 || pack.Episode != 0 {
		t.Fatalf("parsed as %+v", pack)
	}

	if !confirms(pack, Request{Episode: 37, Season: 1}) {
		t.Error("an S01 pack did not confirm an episode of season 1")
	}
	if confirms(pack, Request{Episode: 3, Season: 2}) {
		t.Error("an S01 pack confirmed a season 2 episode")
	}
	if confirms(pack, Request{Episode: 3, Season: 1, Cour: Cour{Later: true}}) {
		t.Error("a season pack confirmed a later cour, which it numbers differently")
	}
	if confirms(parse.Parse("[ASW] Monster [1080p] (Batch)"), Request{Episode: 3, Season: 1}) {
		t.Error("a pack naming no season still confirms nothing")
	}
}
