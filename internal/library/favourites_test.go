package library

import (
	"context"
	"slices"
	"testing"

	"kuro/internal/store"
)

const emptyList = `{"data":{"MediaListCollection":{"lists":[]}}}`

func favSync(t *testing.T, remote ...int) (*Sync, *store.Store, *aniServer) {
	t.Helper()
	up := &aniServer{list: emptyList}
	up.setFavourites(remote...)
	sync, st := newAniSync(t, up)
	return sync, st, up
}

func favourited(t *testing.T, st *store.Store, animeID int) bool {
	t.Helper()
	b, err := st.Bookmark(context.Background(), animeID)
	if err != nil {
		t.Fatal(err)
	}
	return b.Favourite
}

func TestFavouriteMadeHereReachesAniList(t *testing.T) {
	sync, st, up := favSync(t)
	ctx := context.Background()

	if err := st.SetBookmark(ctx, 100, store.Bookmark{Favourite: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := sync.Run(ctx); err != nil {
		t.Fatal(err)
	}

	if got := up.toggles(); !slices.Equal(got, []int{100}) {
		t.Fatalf("toggled %v, want the new favourite pushed once", got)
	}
	if !favourited(t, st, 100) {
		t.Error("the local favourite was cleared by its own sync")
	}

	// Settled: a second run must not flip it back off.
	if _, err := sync.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if got := up.toggles(); len(got) != 1 {
		t.Fatalf("toggled %v, want no further calls once in step", got)
	}
	if !favourited(t, st, 100) {
		t.Error("a second sync unfavourited it")
	}
}

func TestFavouriteFromAniListArrivesHere(t *testing.T) {
	sync, st, _ := favSync(t, 200)
	ctx := context.Background()

	if _, err := sync.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if !favourited(t, st, 200) {
		t.Fatal("a favourite held on the site did not arrive")
	}

	page, err := st.Bookmarks(ctx, store.Paging{Page: 1, PerPage: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != 200 {
		t.Fatalf("bookmarks = %+v, want the pulled favourite listed", page.Items)
	}
}

func TestUnfavouritingOnTheSiteClearsItHere(t *testing.T) {
	sync, st, up := favSync(t, 300)
	ctx := context.Background()

	if _, err := sync.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if !favourited(t, st, 300) {
		t.Fatal("setup: the favourite should have arrived")
	}

	up.setFavourites()
	if _, err := sync.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if favourited(t, st, 300) {
		t.Error("removed on the site but still favourited here")
	}
	if got := up.toggles(); len(got) != 0 {
		t.Errorf("toggled %v, want a pull to send nothing", got)
	}
}

// The merge must never undo a local change that has not been pushed yet.
func TestUnfavouritingHereSurvivesTheMerge(t *testing.T) {
	sync, st, up := favSync(t, 400)
	ctx := context.Background()

	if _, err := sync.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.SetBookmark(ctx, 400, store.Bookmark{Favourite: false}); err != nil {
		t.Fatal(err)
	}
	if _, err := sync.Run(ctx); err != nil {
		t.Fatal(err)
	}

	if favourited(t, st, 400) {
		t.Error("the local removal was undone by the pull")
	}
	if got := up.toggles(); !slices.Equal(got, []int{400}) {
		t.Fatalf("toggled %v, want the removal pushed", got)
	}
	if got := len(up.favourites); got != 0 {
		t.Errorf("site holds %d favourites, want none", got)
	}
}

// Notes and pins live in the same row and have no tracker equivalent.
func TestSyncLeavesTheRestOfTheBookmarkAlone(t *testing.T) {
	sync, st, _ := favSync(t, 500)
	ctx := context.Background()

	if err := st.SetBookmark(ctx, 500, store.Bookmark{
		Favourite: true, Pinned: true, Note: "rewatch with subs",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := sync.Run(ctx); err != nil {
		t.Fatal(err)
	}

	b, err := st.Bookmark(ctx, 500)
	if err != nil {
		t.Fatal(err)
	}
	if !b.Favourite || !b.Pinned || b.Note != "rewatch with subs" {
		t.Fatalf("bookmark = %+v, want everything local kept", b)
	}
}

// Without a connected account the favourites stay exactly as they are.
func TestFavouritesUntouchedWithoutAniList(t *testing.T) {
	up := &aniServer{list: emptyList}
	sync, st := newAniSync(t, up)
	ctx := context.Background()

	if err := st.SetBookmark(ctx, 600, store.Bookmark{Favourite: true}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(ctx, "anilist.user_id", "0"); err != nil {
		t.Fatal(err)
	}
	if _, err := sync.Run(ctx); err != nil {
		t.Fatal(err)
	}

	if !favourited(t, st, 600) {
		t.Error("a local favourite was cleared with no account connected")
	}
	if got := up.toggles(); len(got) != 0 {
		t.Errorf("toggled %v with no account", got)
	}
}
