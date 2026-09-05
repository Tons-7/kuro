package library

import (
	"context"
	"errors"
	"testing"

	"kuro/internal/score"
)

// With no site configured a search is refused outright, with the fix named,
// rather than reported as an episode nobody has released.
func TestFindWithoutSitesSaysSo(t *testing.T) {
	st := bleachStore(t)
	f := NewFinder(st, nil, discard())
	_, err := f.Find(context.Background(), Request{
		AnimeID: tybwCalamity, Episode: 2, Prefs: score.DefaultPreferences(),
	})
	if !errors.Is(err, ErrNoIndexers) {
		t.Fatalf("err = %v, want ErrNoIndexers", err)
	}
}
