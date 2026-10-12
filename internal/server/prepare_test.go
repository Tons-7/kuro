package server

import (
	"io"
	"log/slog"
	"net/http"
	"testing"

	"kuro/internal/config"
	"kuro/internal/library"
)

// The series page readies an episode unless playback is already starting it: two searches could disagree.
func TestPrepareStandsAsideWhileTheEpisodeIsStarting(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	var playback *library.Playback
	h := newHarness(t, config.Config{}, nil, func(d *Deps) {
		playback = library.NewPlayback(d.Store, nil, nil, nil, t.TempDir(), log)
		d.Playback = playback
		d.Prefetch = library.NewPrefetcher(d.Store, nil, nil, log)
	})
	const body = `{"animeId":1,"episode":2}`

	if code, out := h.call(t, "POST", "/api/prepare", body); code != http.StatusAccepted || out["preparing"] != true {
		t.Fatalf("prepare answered %d %v, want accepted", code, out)
	}
	playback.SetStage(1, 2, library.Stage{Step: "search"})
	if code, out := h.call(t, "POST", "/api/prepare", body); code != http.StatusOK || out["preparing"] != false {
		t.Fatalf("prepare while starting answered %d %v, want nothing to do", code, out)
	}
	playback.ClearStage(1, 2)
	if code, _ := h.call(t, "POST", "/api/prepare", body); code != http.StatusAccepted {
		t.Fatalf("prepare after the start answered %d, want accepted again", code)
	}
}
