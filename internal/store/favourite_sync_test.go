package store

import (
	"context"
	"testing"
)

func favouriteStore(t *testing.T) *Store {
	t.Helper()
	st := newTestStore(t)
	if _, err := st.ImportList(context.Background(), []Anime{
		{ID: 100, Romaji: "Sousou no Frieren", Synonyms: "[]", Genres: "[]"},
		{ID: 200, Romaji: "Monster", Synonyms: "[]", Genres: "[]"},
	}, nil, ImportMerge); err != nil {
		t.Fatal(err)
	}
	return st
}

func favouriteRow(t *testing.T, st *Store, id int) (favourite, synced, dirty int) {
	t.Helper()
	err := st.r.QueryRowContext(context.Background(),
		`SELECT favourite, favourite_synced, favourite_dirty FROM user_anime WHERE anime_id = ?`, id).
		Scan(&favourite, &synced, &dirty)
	if err != nil {
		t.Fatal(err)
	}
	return
}

// The push reads the row, sends it, then marks it. A click in between must not
// be stamped as sent.
func TestFavouriteChangedDuringPushStaysDirty(t *testing.T) {
	st := favouriteStore(t)
	ctx := context.Background()

	if err := st.SetBookmark(ctx, 100, Bookmark{Favourite: true}); err != nil {
		t.Fatal(err)
	}
	// Snapshot taken, toggle sent; before it is marked, the user undoes it.
	if err := st.SetBookmark(ctx, 100, Bookmark{Favourite: false}); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkFavouriteSynced(ctx, 100, true); err != nil {
		t.Fatal(err)
	}

	fav, synced, dirty := favouriteRow(t, st, 100)
	if fav != 0 || synced != 1 || dirty != 1 {
		t.Fatalf("row = favourite %d synced %d dirty %d, want the undo kept and still queued", fav, synced, dirty)
	}

	// The merge that follows sees the site holding it, and must not put it back.
	if _, err := st.ApplyRemoteFavourites(ctx, []int{100}); err != nil {
		t.Fatal(err)
	}
	if fav, _, _ := favouriteRow(t, st, 100); fav != 0 {
		t.Error("the merge undid a change still waiting to be pushed")
	}
}

func TestMarkSyncedClearsARowThatDidNotMove(t *testing.T) {
	st := favouriteStore(t)
	ctx := context.Background()

	st.SetBookmark(ctx, 100, Bookmark{Favourite: true})
	if err := st.MarkFavouriteSynced(ctx, 100, true); err != nil {
		t.Fatal(err)
	}
	if fav, synced, dirty := favouriteRow(t, st, 100); fav != 1 || synced != 1 || dirty != 0 {
		t.Fatalf("row = favourite %d synced %d dirty %d", fav, synced, dirty)
	}
}

// A sync that changes nothing must not touch the rows: the favourites page
// orders by updated_at, and every sync used to shuffle it.
func TestMergeLeavesSettledRowsAlone(t *testing.T) {
	st := favouriteStore(t)
	ctx := context.Background()

	if _, err := st.ApplyRemoteFavourites(ctx, []int{100, 200}); err != nil {
		t.Fatal(err)
	}
	var before []int64
	for _, id := range []int{100, 200} {
		var at int64
		st.r.QueryRowContext(ctx, `SELECT updated_at FROM user_anime WHERE anime_id = ?`, id).Scan(&at)
		before = append(before, at)
	}
	if _, err := st.w.ExecContext(ctx, `UPDATE user_anime SET updated_at = updated_at - 1000`); err != nil {
		t.Fatal(err)
	}

	if _, err := st.ApplyRemoteFavourites(ctx, []int{100, 200}); err != nil {
		t.Fatal(err)
	}
	for i, id := range []int{100, 200} {
		var at int64
		st.r.QueryRowContext(ctx, `SELECT updated_at FROM user_anime WHERE anime_id = ?`, id).Scan(&at)
		if at != before[i]-1000 {
			t.Errorf("anime %d: updated_at moved on a sync that changed nothing", id)
		}
	}
}

func TestMergeAddsAndRemovesOnlyWhatChanged(t *testing.T) {
	st := favouriteStore(t)
	ctx := context.Background()

	if _, err := st.ApplyRemoteFavourites(ctx, []int{100}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApplyRemoteFavourites(ctx, []int{200}); err != nil {
		t.Fatal(err)
	}
	if fav, _, _ := favouriteRow(t, st, 100); fav != 0 {
		t.Error("removed on the site but still favourited here")
	}
	if fav, synced, dirty := favouriteRow(t, st, 200); fav != 1 || synced != 1 || dirty != 0 {
		t.Errorf("new site favourite = favourite %d synced %d dirty %d", fav, synced, dirty)
	}
}

func TestUnhydratedFindsPlaceholdersAndMissingRows(t *testing.T) {
	st := favouriteStore(t)
	ctx := context.Background()

	if err := st.EnsureAnime(ctx, 300); err != nil {
		t.Fatal(err)
	}
	got, err := st.Unhydrated(ctx, []int{100, 300, 400})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != 300 || got[1] != 400 {
		t.Fatalf("unhydrated = %v, want the placeholder and the missing id", got)
	}
}
