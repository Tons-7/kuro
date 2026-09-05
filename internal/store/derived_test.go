package store

import (
	"context"
	"testing"

	"kuro/internal/metadata"
)

// A show no metadata source has catalogued gets rows invented from its episode
// count. Releases are the only real evidence of which episodes exist.
func TestDerivedEpisodesReplaceInventedRows(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedCatalogue(t, s, 208685, "RELEASING", 8)

	before, err := s.Episodes(ctx, 208685)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 8 || !before[0].Planned {
		t.Fatalf("expected 8 invented rows, got %d", len(before))
	}

	// Four have aired and been released; the rest have not.
	saved, err := s.SaveDerivedEpisodes(ctx, 208685, []int{1, 2, 3, 4})
	if err != nil {
		t.Fatal(err)
	}
	if saved != 4 {
		t.Errorf("saved %d, want 4", saved)
	}

	after, err := s.Episodes(ctx, 208685)
	if err != nil {
		t.Fatal(err)
	}
	// Still eight: four evidenced, four padded from the catalogue count, so a
	// show mid-season does not appear to end at its latest release.
	if len(after) != 8 {
		t.Fatalf("got %d episodes, want 8", len(after))
	}
	for i, e := range after[:4] {
		if e.Planned {
			t.Errorf("episode %d has a release and should not be marked planned", i+1)
		}
	}
	for i, e := range after[4:] {
		if !e.Planned {
			t.Errorf("episode %d has no release and should be planned", i+5)
		}
	}
}

// Padding is only for release-derived lists: a real source lists unaired
// episodes itself, and inventing more on top would duplicate them.
func TestPaddingLeavesRealMetadataAlone(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedCatalogue(t, s, 300, "RELEASING", 12)

	if _, err := s.SaveEpisodes(ctx, 300, []metadata.Episode{
		{Number: 1, TitleEN: "One"},
		{Number: 2, TitleEN: "Two"},
	}); err != nil {
		t.Fatal(err)
	}

	eps, err := s.Episodes(ctx, 300)
	if err != nil {
		t.Fatal(err)
	}
	if len(eps) != 2 {
		t.Errorf("got %d episodes, want the 2 that were fetched", len(eps))
	}
}

// The search costs seconds against every indexer, so it runs once a day.
func TestDerivedIsRememberedEvenWhenNothingWasFound(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedCatalogue(t, s, 400, "FINISHED", 3)

	if s.EpisodesDerived(ctx, 400) {
		t.Fatal("nothing has been searched yet")
	}
	if _, err := s.SaveDerivedEpisodes(ctx, 400, nil); err != nil {
		t.Fatal(err)
	}
	if !s.EpisodesDerived(ctx, 400) {
		t.Error("an empty result must still count as searched")
	}
}
