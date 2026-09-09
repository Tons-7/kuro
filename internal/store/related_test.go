package store

import (
	"context"
	"testing"
	"time"
)

// The films, OVAs and spin-offs a franchise links to, which used to be
// discarded because only prequel/sequel edges were kept.
func TestRelatedListsEverythingThatIsNotASeason(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for _, id := range []int{1, 2, 3, 10, 11, 12} {
		seedCatalogue(t, s, id, "FINISHED", 12)
	}

	// 1 → 2 → 3 are seasons; the rest hang off season 1.
	if err := s.SaveRelations(ctx, []Relation{
		{AnimeID: 1, RelatedID: 2, Kind: "SEQUEL"},
		{AnimeID: 2, RelatedID: 1, Kind: "PREQUEL"},
		{AnimeID: 2, RelatedID: 3, Kind: "SEQUEL"},
		{AnimeID: 3, RelatedID: 2, Kind: "PREQUEL"},
		{AnimeID: 1, RelatedID: 10, Kind: "SIDE_STORY"},
		{AnimeID: 1, RelatedID: 11, Kind: "SPIN_OFF"},
		{AnimeID: 2, RelatedID: 12, Kind: "SUMMARY"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RebuildFranchises(ctx); err != nil {
		t.Fatal(err)
	}

	related, err := s.Related(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	got := map[int]string{}
	for _, e := range related {
		got[e.ID] = e.Kind
	}
	if len(got) != 3 {
		t.Fatalf("related = %+v, want the three non-season entries", got)
	}
	for id, kind := range map[int]string{10: "SIDE_STORY", 11: "SPIN_OFF", 12: "SUMMARY"} {
		if got[id] != kind {
			t.Errorf("%d = %q, want %q", id, got[id], kind)
		}
	}

	// Asking from any season gives the same list, and never the seasons.
	fromLast, err := s.Related(ctx, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(fromLast) != 3 {
		t.Errorf("from the last season: %+v", fromLast)
	}
	for _, e := range fromLast {
		if e.ID == 1 || e.ID == 2 || e.ID == 3 {
			t.Errorf("a season appeared in related: %d", e.ID)
		}
	}
}

// Relations were fetched once and never again, so a film announced later never
// showed up. Freshness decides when the graph is walked a second time.
func TestRelationFreshness(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if !s.RelationsStale(ctx, 1, time.Hour) {
		t.Error("never fetched should be stale")
	}
	if err := s.MarkRelationsFetched(ctx, []int{1, 2}); err != nil {
		t.Fatal(err)
	}
	if s.RelationsStale(ctx, 1, time.Hour) {
		t.Error("just fetched should be fresh")
	}
	if !s.RelationsStale(ctx, 1, 0) {
		t.Error("any age is stale against a zero window")
	}
	if s.RelationsStale(ctx, 2, time.Hour) {
		t.Error("every id walked is marked, not only the one asked for")
	}
}
