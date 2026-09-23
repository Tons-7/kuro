package transcode

import (
	"os"
	"testing"
	"time"
)

// An earlier pass left 8 behind past a gap. The new pass writing 7 must not
// count 7 as complete on the strength of that stale 8.
func TestStaleSegmentPastAGapIsNoProof(t *testing.T) {
	s := &Session{dir: t.TempDir(), Segments: 20}
	write := func(n int, at time.Time) {
		p := s.SegmentPath(n)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		os.Chtimes(p, at, at)
	}
	old := time.Now().Add(-time.Minute)
	write(8, old)

	s.run = &run{from: 0, began: time.Now().Add(-time.Second)}
	s.headTo = 6
	write(7, time.Now())

	s.advanceHead()
	if s.headTo != 6 {
		t.Fatalf("headTo = %d; a stale 8 marked the half-written 7 done", s.headTo)
	}
	if s.nextStarted(7) {
		t.Fatal("WaitSegment would serve 7 before it is finished")
	}

	write(8, time.Now())
	if !s.nextStarted(7) {
		t.Fatal("this pass's own 8 must count")
	}
}
