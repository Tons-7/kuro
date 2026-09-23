package transcode

import "testing"

func TestDropMirror(t *testing.T) {
	sub := func(i int, lang string) Stream {
		return Stream{Index: i, Kind: "subtitle", Codec: "ass", Language: lang, Title: "CR"}
	}

	doubled := []Stream{sub(2, "eng"), sub(3, "por"), sub(13, "eng"), sub(14, "por")}
	if got := dropMirror(doubled); len(got) != 2 || got[1].Index != 3 {
		t.Errorf("mirror kept: %+v", got)
	}

	// Two tracks that only look alike in part are both real.
	real := []Stream{sub(2, "eng"), sub(3, "eng")}
	real[1].Title = "Signs"
	if got := dropMirror(real); len(got) != 2 {
		t.Errorf("real track dropped: %+v", got)
	}
	// A copy sits at a later index; the same index twice is not one.
	same := []Stream{{Index: 1, Kind: "audio", Codec: "aac", Language: "jpn"}, {Index: 1, Kind: "audio", Codec: "aac", Language: "jpn"}}
	if got := dropMirror(same); len(got) != 2 {
		t.Errorf("same index is not a mirror: %+v", got)
	}
}
