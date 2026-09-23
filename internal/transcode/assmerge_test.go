package transcode

import (
	"strings"
	"testing"
)

const mergeHead = "[Script Info]\nScriptType: v4.00+\n\n[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n"

// Reads from the start and around the playhead recover different stretches;
// the track is both, once each, in time order.
func TestMergeASSUnionsInTimeOrder(t *testing.T) {
	start := mergeHead +
		"Dialogue: 0,0:00:01.00,0:00:02.00,Default,,0,0,0,,one\n" +
		"Dialogue: 0,0:00:05.00,0:00:06.00,Default,,0,0,0,,two\n"
	window := mergeHead +
		"Dialogue: 0,0:04:00.00,0:04:02.00,Default,,0,0,0,,late\n" +
		"Dialogue: 0,0:00:05.00,0:00:06.00,Default,,0,0,0,,two\n" +
		"Dialogue: 0,0:03:59.00,0:04:00.00,Default,,0,0,0,,before late\n"

	got := mergeASS(start, window)
	_, events := splitASS(got)
	want := []string{"one", "two", "before late", "late"}
	if len(events) != len(want) {
		t.Fatalf("events = %q", events)
	}
	for i, w := range want {
		if !strings.HasSuffix(events[i], ","+w) {
			t.Errorf("event %d = %q, want %q", i, events[i], w)
		}
	}
	if !strings.HasPrefix(got, "[Script Info]") || strings.Count(got, "[Events]") != 1 {
		t.Errorf("header mangled:\n%s", got)
	}
	// Merging what is already there changes nothing.
	if again := mergeASS(got, window); again != got {
		t.Error("merging a read already included changed the track")
	}
}

func TestMergeASSIntoNothingIsTheRead(t *testing.T) {
	if got := mergeASS("", mergeHead); got != mergeHead {
		t.Errorf("got %q", got)
	}
}

// Ten hours sorts after nine.
func TestEventStartSortsHours(t *testing.T) {
	a := eventStart("Dialogue: 0,9:59:59.00,x")
	b := eventStart("Dialogue: 0,10:00:00.00,x")
	if a >= b {
		t.Errorf("%q !< %q", a, b)
	}
}
