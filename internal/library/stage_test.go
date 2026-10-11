package library

import "testing"

// Several releases are tried at once; the wait shown is the best-ranked one's, not whichever spoke last.
func TestStageReportsTheBestRankedCandidate(t *testing.T) {
	p := &Playback{}
	p.SetStage(1, 1, Stage{Step: "search"})
	p.setStage(1, 1, 2, Stage{Step: "files", Release: "third"})
	p.setStage(1, 1, 0, Stage{Step: "data", Release: "first", Peers: 3})
	p.setStage(1, 1, 2, Stage{Step: "data", Release: "third"})

	if s, ok := p.Stage(1, 1); !ok || s.Release != "first" || s.Step != "data" || s.Peers != 3 {
		t.Fatalf("stage = %+v, ok %v", s, ok)
	}

	// The best one failing hands the word to whoever reports next.
	p.dropStage(1, 1, 0)
	if s, _ := p.Stage(1, 1); s.Step != "search" {
		t.Errorf("after the first failed: %+v", s)
	}
	p.setStage(1, 1, 2, Stage{Step: "data", Release: "third"})
	if s, _ := p.Stage(1, 1); s.Release != "third" {
		t.Errorf("the next candidate was not reported: %+v", s)
	}
}

// A lower-ranked candidate failing must not wipe what the better one is waiting on.
func TestStageSurvivesALowerCandidateFailing(t *testing.T) {
	p := &Playback{}
	p.setStage(1, 1, 0, Stage{Step: "data", Release: "first"})
	p.dropStage(1, 1, 2)
	if s, _ := p.Stage(1, 1); s.Release != "first" {
		t.Errorf("stage = %+v", s)
	}
}

// One episode's wait is not another's, and it is gone once the episode has started.
func TestStageIsPerEpisodeAndCleared(t *testing.T) {
	p := &Playback{}
	p.SetStage(1, 1, Stage{Step: "start"})
	if _, ok := p.Stage(1, 2); ok {
		t.Error("another episode reported a stage")
	}
	p.ClearStage(1, 1)
	if _, ok := p.Stage(1, 1); ok {
		t.Error("the stage outlived the start")
	}
	// Nothing playing at all is not an error.
	var none *Playback
	if _, ok := none.Stage(1, 1); ok {
		t.Error("a nil playback reported a stage")
	}
}
