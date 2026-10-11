package library

import "fmt"

// Stage is what a starting episode is waiting on, so the player can say it instead of a bare spinner.
type Stage struct {
	// Step: "search" for releases, "files" for a release's file list, "data" for its first bytes,
	// "start" for the start of the file to be read.
	Step    string `json:"step"`
	Release string `json:"release,omitempty"`
	Peers   int    `json:"peers,omitempty"`
	Bytes   int64  `json:"bytes,omitempty"`

	// Which candidate this is about: of several tried at once, the best-ranked one is reported.
	rank int
}

func stageKey(animeID, episode int) string { return fmt.Sprintf("%d/%d", animeID, episode) }

// SetStage records what the episode is waiting on now.
func (p *Playback) SetStage(animeID, episode int, s Stage) { p.setStage(animeID, episode, 0, s) }

// setStage keeps the best-ranked candidate's stage: a lower-ranked one racing beside it says nothing.
func (p *Playback) setStage(animeID, episode, rank int, s Stage) {
	p.stageMu.Lock()
	defer p.stageMu.Unlock()
	if p.stages == nil {
		p.stages = map[string]Stage{}
	}
	key := stageKey(animeID, episode)
	if have, ok := p.stages[key]; ok && have.Step != "search" && have.rank < rank {
		return
	}
	s.rank = rank
	p.stages[key] = s
}

// dropStage lets the next candidate speak once this one has failed.
func (p *Playback) dropStage(animeID, episode, rank int) {
	p.stageMu.Lock()
	defer p.stageMu.Unlock()
	key := stageKey(animeID, episode)
	if have, ok := p.stages[key]; ok && have.rank == rank {
		p.stages[key] = Stage{Step: "search"}
	}
}

// ClearStage forgets the stage once the episode has started or failed.
func (p *Playback) ClearStage(animeID, episode int) {
	p.stageMu.Lock()
	defer p.stageMu.Unlock()
	delete(p.stages, stageKey(animeID, episode))
}

// Stage reports what the episode is waiting on, false when it is not starting.
func (p *Playback) Stage(animeID, episode int) (Stage, bool) {
	if p == nil {
		return Stage{}, false
	}
	p.stageMu.Lock()
	defer p.stageMu.Unlock()
	s, ok := p.stages[stageKey(animeID, episode)]
	return s, ok
}
