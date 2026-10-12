package library

import (
	"context"
	"testing"

	"kuro/internal/player"
	"kuro/internal/sound"
)

// capturingPlayer keeps the options it was launched with.
type capturingPlayer struct {
	fakePlayer
	opts player.Options
}

func (c *capturingPlayer) Play(_ context.Context, opts player.Options) error {
	c.opts = opts
	return nil
}

// The sound settings reach a desktop player the way they reach the browser.
func TestDesktopPlayersGetTheSoundSettings(t *testing.T) {
	st := prefetchStore(t)
	ctx := context.Background()
	for key, value := range map[string]string{"playback.voice": "high", "playback.night": "true", "playback.mono": "true", "playback.boost": "medium"} {
		if err := st.SetSetting(ctx, key, value); err != nil {
			t.Fatal(err)
		}
	}
	mpv := &capturingPlayer{}
	p := NewPlayback(st, nil, nil, mpv, t.TempDir(), discard())
	if err := st.EnsureAnime(ctx, 1); err != nil {
		t.Fatal(err)
	}

	if err := p.launch(ctx, PlayRequest{AnimeID: 1, Episode: 1}, &Session{StreamURL: "http://127.0.0.1:1/s"}); err != nil {
		t.Fatal(err)
	}
	want := sound.Options{Voice: "high", Night: true, Mono: true, Boost: "medium"}
	if mpv.opts.Sound != want {
		t.Fatalf("player got %+v, want %+v", mpv.opts.Sound, want)
	}
}
