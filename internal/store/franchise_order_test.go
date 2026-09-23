package store

import (
	"context"
	"testing"
)

// A sequel the walk found but no row describes yet must not become season one.
func TestUnknownMemberSortsLast(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedCatalogue(t, s, 50, "FINISHED", 12)
	seedCatalogue(t, s, 60, "FINISHED", 12)
	s.w.ExecContext(ctx, `UPDATE anime SET start_date = '2020-01-01' WHERE id = 50`)
	s.w.ExecContext(ctx, `UPDATE anime SET start_date = '2022-01-01' WHERE id = 60`)

	if err := s.SaveRelations(ctx, []Relation{
		{AnimeID: 50, RelatedID: 60, Kind: "SEQUEL"},
		{AnimeID: 60, RelatedID: 7, Kind: "SEQUEL"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RebuildFranchises(ctx); err != nil {
		t.Fatal(err)
	}

	want := map[int]int{50: 1, 60: 2, 7: 3}
	for id, ordinal := range want {
		var got, root int
		if err := s.r.QueryRowContext(ctx,
			`SELECT ordinal, root_id FROM franchise WHERE anime_id = ?`, id).Scan(&got, &root); err != nil {
			t.Fatalf("%d: %v", id, err)
		}
		if got != ordinal || root != 50 {
			t.Errorf("%d: ordinal %d root %d, want %d root 50", id, got, root, ordinal)
		}
	}
}
