package player

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"
)

func TestVLCArgs(t *testing.T) {
	v := NewVLC("", slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctl := control{port: 8089, password: "pw"}
	args := v.args(Options{URL: "http://127.0.0.1:3030/s", Title: "Ep 3", StartAt: 61.5, Audio: "dub"}, ctl)

	if last := args[len(args)-1]; last != "http://127.0.0.1:3030/s" {
		t.Errorf("the stream must come last, got %q", last)
	}
	for _, want := range []string{
		"--no-one-instance", "--extraintf=http", "--http-host=127.0.0.1", "--http-port=8089", "--http-password=pw",
		"--meta-title=Ep 3", "--start-time=61.500", "--audio-language=eng,en,english",
	} {
		if !slices.Contains(args, want) {
			t.Errorf("missing %q in %v", want, args)
		}
	}
	if slices.Contains(args, "--play-and-exit") {
		t.Error("the end is read from the interface; VLC must not quit on its own")
	}
	for _, a := range v.args(Options{URL: "x"}, ctl) {
		if strings.HasPrefix(a, "--start") || strings.HasPrefix(a, "--audio") {
			t.Errorf("no start, no audio: %v", a)
		}
	}
}

func TestVLCRefusesToStartWhenMissing(t *testing.T) {
	v := NewVLC("", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := v.Play(context.Background(), Options{URL: "x"}); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Errorf("err = %v", err)
	}
	if v.Running() {
		t.Error("nothing should be running")
	}
}

func TestSkipTargetDoesNotLoopOnItsOwnBoundary(t *testing.T) {
	ranges := []SkipRange{{Kind: "op", Start: 10, End: 100}, {Kind: "ed", Start: 1300, End: 1390}}
	if r, ok := skipTarget(ranges, 50, -1); !ok || r.Kind != "op" {
		t.Errorf("inside the opening: %v %v", r, ok)
	}
	if _, ok := skipTarget(ranges, 100, 100); ok {
		t.Error("landing on the end of the range just skipped must not skip again")
	}
	if _, ok := skipTarget(ranges, 500, 100); ok {
		t.Error("outside every range")
	}
}
