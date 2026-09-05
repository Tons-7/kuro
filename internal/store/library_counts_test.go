package store

import (
	"context"
	"testing"
)

func TestLibraryCountsPerStatusAndFavourites(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	eps := 12
	if _, err := s.ImportList(ctx, []Anime{
		{ID: 1, Romaji: "A", Synonyms: "[]", Genres: "[]", Episodes: &eps},
		{ID: 2, Romaji: "B", Synonyms: "[]", Genres: "[]", Episodes: &eps},
		{ID: 3, Romaji: "C", Synonyms: "[]", Genres: "[]", Episodes: &eps},
	}, nil, ImportMerge); err != nil {
		t.Fatal(err)
	}
	for id, status := range map[int]string{1: "CURRENT", 2: "CURRENT", 3: "COMPLETED"} {
		if err := s.SetListStatus(ctx, id, status, -1); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.ApplyRemoteFavourites(ctx, []int{3}); err != nil {
		t.Fatal(err)
	}

	got, err := s.LibraryCounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != 3 || got.Statuses["CURRENT"] != 2 || got.Statuses["COMPLETED"] != 1 || got.Favourites != 1 {
		t.Fatalf("counts = %+v", got)
	}
}

func TestLibraryCountsOnAnEmptyList(t *testing.T) {
	s := newTestStore(t)

	got, err := s.LibraryCounts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != 0 || len(got.Statuses) != 0 || got.Favourites != 0 {
		t.Fatalf("counts = %+v", got)
	}
}
