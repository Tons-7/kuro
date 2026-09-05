package deps

import (
	"context"
	"io"
	"log/slog"
	"testing"
)

func TestLatestKnownIsEmptyUntilResolved(t *testing.T) {
	m := New(t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if got := m.LatestKnown("rqbit"); got != "" {
		t.Errorf("LatestKnown = %q before anything was resolved", got)
	}
}

func TestLatestRefusesAnUnknownComponent(t *testing.T) {
	m := New(t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, err := m.Latest(context.Background(), "photoshop"); err == nil {
		t.Fatal("an unknown component resolved a version")
	}
}

func TestInstallRefusesAnUnknownComponentBeforeAnyHook(t *testing.T) {
	m := New(t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	called := false
	m.OnInstalling(func(string) { called = true })
	if err := m.Install("photoshop"); err == nil {
		t.Fatal("an unknown component started installing")
	}
	if called {
		t.Error("the pre-install hook ran for a component that does not exist")
	}
}
