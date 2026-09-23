package library

import (
	"context"
	"testing"

	"kuro/internal/anilist"
	"kuro/internal/player"
	"kuro/internal/store"
)

type fakePlayer struct {
	events  chan player.Event
	stopped int
}

func (f *fakePlayer) Play(context.Context, player.Options) error { return nil }
func (f *fakePlayer) Events() <-chan player.Event                { return f.events }
func (f *fakePlayer) Running() bool                              { return false }
func (f *fakePlayer) Stop()                                      { f.stopped++ }

// Dragging to the end and letting mpv reach EOF is a peek, not a watch; the
// same end after actually playing it through is.
func TestMpvEOFFollowsThePlayedShareRule(t *testing.T) {
	ctx := context.Background()
	run := func(positions []float64) int {
		st := prefetchStore(t)
		episodes := 12
		st.ImportList(ctx, []store.Anime{{ID: 9, Romaji: "Show", Synonyms: "[]", Genres: "[]", Episodes: &episodes}}, nil, store.ImportMerge)
		p := NewPlayback(st, nil, nil, &fakePlayer{}, t.TempDir(), discard()).
			WithSync(NewSync(st, anilist.New(discard()), discard()))

		pl := &fakePlayer{events: make(chan player.Event, len(positions)+1)}
		for _, pos := range positions {
			pl.events <- player.Event{Kind: player.EventPosition, Position: pos, Duration: 20}
		}
		pl.events <- player.Event{Kind: player.EventEnd, Reason: "eof"}
		close(pl.events)
		p.track(pl, 9, 1, 1, p.trackGen.Load())

		progress, _ := st.ListProgress(ctx)
		return progress[9]
	}

	if got := run([]float64{1, 19.5}); got != 0 {
		t.Errorf("seek to the end counted: progress = %d", got)
	}
	var through []float64
	for s := 1.0; s <= 19; s++ {
		through = append(through, s)
	}
	if got := run(through); got != 1 {
		t.Errorf("played through: progress = %d, want 1", got)
	}
}

// The preference names the desktop player; anything unregistered means mpv.
func TestExternalPlayerFollowsThePreference(t *testing.T) {
	st := prefetchStore(t)
	ctx := context.Background()
	mpv, vlc := &fakePlayer{}, &fakePlayer{}
	p := NewPlayback(st, nil, nil, mpv, t.TempDir(), discard()).WithPlayer("vlc", vlc)

	if name, pl := p.external(ctx); name != "mpv" || pl != Player(mpv) {
		t.Errorf("default = %s", name)
	}
	if err := st.SetSetting(ctx, "playback.player", "vlc"); err != nil {
		t.Fatal(err)
	}
	if name, pl := p.external(ctx); name != "vlc" || pl != Player(vlc) {
		t.Errorf("vlc chosen, got %s", name)
	}
	if err := st.SetSetting(ctx, "playback.player", "browser"); err != nil {
		t.Fatal(err)
	}
	if name, _ := p.external(ctx); name != "mpv" {
		t.Errorf("browser has no desktop player; mpv stands in, got %s", name)
	}

	p.StopPlayers()
	if mpv.stopped != 1 || vlc.stopped != 1 {
		t.Errorf("stop reached mpv %d, vlc %d times", mpv.stopped, vlc.stopped)
	}
}
