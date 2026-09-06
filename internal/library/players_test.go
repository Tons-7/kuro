package library

import (
	"context"
	"testing"

	"kuro/internal/player"
)

type fakePlayer struct {
	events  chan player.Event
	stopped int
}

func (f *fakePlayer) Play(context.Context, player.Options) error { return nil }
func (f *fakePlayer) Events() <-chan player.Event                { return f.events }
func (f *fakePlayer) Running() bool                              { return false }
func (f *fakePlayer) Stop()                                      { f.stopped++ }

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
