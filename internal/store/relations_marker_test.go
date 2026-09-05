package store

import (
	"context"
	"testing"
)

// A standalone show has nothing to save, so without a marker it is looked up
// against AniList on every visit. The marker also has to survive the franchise
// rebuild that any other show's fetch triggers.
func TestNoRelationsMarkerSurvivesARebuild(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedShow(t, s, 1, 12)

	if known, _ := s.HasRelations(ctx, 1); known {
		t.Fatal("nothing recorded yet")
	}
	if err := s.MarkNoRelations(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if known, _ := s.HasRelations(ctx, 1); !known {
		t.Fatal("the marker was not recorded")
	}

	// Another show's relations are fetched, which rebuilds the franchise table.
	if _, err := s.RebuildFranchises(ctx); err != nil {
		t.Fatal(err)
	}
	if known, _ := s.HasRelations(ctx, 1); !known {
		t.Fatal("the marker did not survive the rebuild")
	}

	// And it must not invent a season chain for the show.
	seasons, err := s.Franchise(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(seasons.Seasons) > 1 {
		t.Fatalf("standalone show reports %d seasons", len(seasons.Seasons))
	}
}
