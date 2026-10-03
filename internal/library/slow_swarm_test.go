package library

import (
	"context"
	"testing"
	"time"

	"kuro/internal/indexer"
	"kuro/internal/score"
)

// Timings shrunk to milliseconds.
func quickWarm(t *testing.T) {
	t.Helper()
	probe, poll, stall, stagger := peerProbeDeadline, headPoll, headStall, raceStagger
	peerProbeDeadline, headPoll, headStall, raceStagger = 50*time.Millisecond, 10*time.Millisecond, 150*time.Millisecond, 20*time.Millisecond
	t.Cleanup(func() {
		peerProbeDeadline, headPoll, headStall, raceStagger = probe, poll, stall, stagger
	})
}

// A slow release plays as long as bytes keep arriving.
func TestSlowButDeliveringReleasePlays(t *testing.T) {
	quickWarm(t)
	engine := newFakeEngine()
	engine.trickle[goodHash] = 600 * time.Millisecond // four stall windows

	p := newPlayback(t, engine, []indexer.Torrent{
		release(goodHash, "[Slow] Sousou no Frieren - 01 [1080p].mkv", 40),
	})
	rel, _, _, _, _, err := p.attach(context.Background(), PlayRequest{
		AnimeID: 1, Episode: 1, Season: 1, Prefs: score.DefaultPreferences(),
	})
	if err != nil {
		t.Fatalf("a delivering release was given up on: %v", err)
	}
	if rel.Torrent.InfoHash != goodHash || engine.wasDeleted(goodHash) {
		t.Fatalf("picked %s, deleted=%v", rel.Torrent.InfoHash, engine.wasDeleted(goodHash))
	}
}

// Peers that never send a byte: move on.
func TestStalledReleaseGivesWay(t *testing.T) {
	quickWarm(t)
	engine := newFakeEngine()
	engine.stuck[deadHash] = true

	p := newPlayback(t, engine, []indexer.Torrent{
		release(deadHash, "[Stuck] Sousou no Frieren - 01 [1080p].mkv", 900),
		release(goodHash, "[Live] Sousou no Frieren - 01 [1080p].mkv", 50),
	})
	start := time.Now()
	rel, _, _, _, _, err := p.attach(context.Background(), PlayRequest{
		AnimeID: 1, Episode: 1, Season: 1, Prefs: score.DefaultPreferences(),
	})
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	if rel.Torrent.InfoHash != goodHash {
		t.Fatalf("picked %s, want the live one", rel.Torrent.InfoHash)
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("waited %s on a stalled release", took)
	}
	if !engine.wasDeleted(deadHash) {
		t.Error("the stalled release was left downloading")
	}
}

func withPatience(t *testing.T, d time.Duration) {
	t.Helper()
	old := racePatience
	racePatience = d
	t.Cleanup(func() { racePatience = old })
}

// A better release still loading after the patience window loses to a ready one.
func TestReadyReleasePlaysWhenTheBetterOneIsSlow(t *testing.T) {
	quickWarm(t)
	withPatience(t, 200*time.Millisecond)
	engine := newFakeEngine()
	engine.trickle[goodHash] = 5 * time.Second

	p := newPlayback(t, engine, []indexer.Torrent{
		release(goodHash, "[Best] Sousou no Frieren - 01 [1080p BluRay].mkv", 900),
		release(otherHash, "[Worse] Sousou no Frieren - 01 [720p].mkv", 50),
	})
	start := time.Now()
	rel, _, _, _, _, err := p.attach(context.Background(), PlayRequest{
		AnimeID: 1, Episode: 1, Season: 1, Prefs: score.DefaultPreferences(),
	})
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	if rel.Torrent.InfoHash != otherHash {
		t.Fatalf("picked %s, want the ready one", rel.Torrent.InfoHash)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("waited %s on the slow one", took)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !engine.wasDeleted(goodHash) {
		time.Sleep(20 * time.Millisecond)
	}
	if !engine.wasDeleted(goodHash) {
		t.Error("the slow release kept downloading after losing")
	}
}

// Within the patience window, the better release still wins.
func TestBetterReleaseWinsWithinPatience(t *testing.T) {
	quickWarm(t)
	withPatience(t, 2*time.Second)
	engine := newFakeEngine()
	engine.trickle[goodHash] = 400 * time.Millisecond

	p := newPlayback(t, engine, []indexer.Torrent{
		release(goodHash, "[Best] Sousou no Frieren - 01 [1080p BluRay].mkv", 900),
		release(otherHash, "[Worse] Sousou no Frieren - 01 [720p].mkv", 50),
	})
	rel, _, _, _, _, err := p.attach(context.Background(), PlayRequest{
		AnimeID: 1, Episode: 1, Season: 1, Prefs: score.DefaultPreferences(),
	})
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	if rel.Torrent.InfoHash != goodHash {
		t.Fatalf("picked %s, want the better one", rel.Torrent.InfoHash)
	}
}
