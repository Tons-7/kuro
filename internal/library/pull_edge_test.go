package library

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"kuro/internal/anilist"
	"kuro/internal/db"
	"kuro/internal/mal"
	"kuro/internal/store"
)

func TestAniListRunSkipsEverythingWhenNotConnected(t *testing.T) {
	up := &aniServer{list: `{"data":{"MediaListCollection":{"lists":[]}}}`}
	sync, st := newAniSync(t, up)
	sync.al.SetToken("")
	st.MarkWatched(context.Background(), 100, 1)

	rep, err := sync.Run(context.Background())
	if err != nil || rep != (SyncReport{}) {
		t.Fatalf("report=%+v err=%v", rep, err)
	}
	if len(up.saves) != 0 {
		t.Fatal("pushed without a token")
	}
}

func TestAniListRunWithoutImporterOnlyPushes(t *testing.T) {
	up := &aniServer{list: `{"data":{"MediaListCollection":{"lists":[
      {"name":"W","entries":[{"id":1,"mediaId":200,"progress":1,"media":{"id":200,"title":{"romaji":"X"}}}]}]}}}`}
	sync, st := newAniSync(t, up)
	sync.importer = nil
	ctx := context.Background()
	st.ImportList(ctx, []store.Anime{{ID: 100, Romaji: "Frieren"}}, nil, store.ImportMerge)
	st.MarkWatched(ctx, 100, 2)

	rep, err := sync.Run(ctx)
	if err != nil || rep.Pushed != 1 || rep.Pulled != 0 {
		t.Fatalf("report=%+v err=%v", rep, err)
	}
	if e, _ := st.ListEntry(ctx, 200); e.AnimeID != 0 {
		t.Fatal("pulled without an importer")
	}
}

// A rejected token ends the run before the pull: every entry retried would
// only burn the rate limit.
func TestAniListRunStopsOnUnauthorized(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		io.WriteString(w, `{"errors":[{"message":"Invalid token","status":400}]}`)
	}))
	defer srv.Close()
	conn, _ := db.Open(filepath.Join(t.TempDir(), "test.db"))
	defer conn.Close()
	conn.Migrate()
	st := store.New(conn)
	ctx := context.Background()
	st.SetSetting(ctx, "anilist.user_id", "1")
	st.ImportList(ctx, []store.Anime{{ID: 1, Romaji: "A"}, {ID: 2, Romaji: "B"}}, nil, store.ImportMerge)
	st.MarkWatched(ctx, 1, 1)
	st.MarkWatched(ctx, 2, 1)

	al := anilist.New(discard(), anilist.WithEndpoint(srv.URL), anilist.WithoutRateLimit())
	al.SetToken("bad")
	sync := NewSync(st, al, discard()).WithImporter(NewImporter(st, al, discard()))

	rep, err := sync.Run(ctx)
	if err != nil || rep.Failed != 1 || rep.Pulled != 0 {
		t.Fatalf("report=%+v err=%v", rep, err)
	}
	if calls != 1 {
		t.Fatalf("%d upstream calls, want the one that was refused", calls)
	}
	if dirty, _ := st.DirtyEntries(ctx, 0); len(dirty) != 2 {
		t.Fatalf("dirty = %+v, want both kept for a later push", dirty)
	}
}

func TestMALPullWhenNotConnectedIsANoOp(t *testing.T) {
	up := &malServer{list: `{"data":[{"node":{"id":52991},"list_status":{"status":"watching","num_episodes_watched":3}}],"paging":{}}`}
	sync, _ := newMALSync(t, up)
	sync.mal.SetToken(mal.Token{})
	rep, err := sync.Pull(context.Background())
	if err != nil || rep != (MALImport{}) {
		t.Fatalf("report=%+v err=%v", rep, err)
	}
}

// First connection with a local list already ahead of MAL: the site's state
// is recorded, not applied, and the next push corrects MAL.
func TestMALPullSeedsWithoutApplyingWhenLocalExists(t *testing.T) {
	up := &malServer{list: `{"data":[
	  {"node":{"id":52991,"title":"Frieren","num_episodes":28},
	   "list_status":{"status":"watching","num_episodes_watched":2,
	                  "updated_at":"2026-08-01T00:00:00+00:00"}}],"paging":{}}`}
	sync, st := newMALSync(t, up)
	ctx := context.Background()
	addAnime(t, st, 154587, 52991, 28)
	st.MarkWatched(ctx, 154587, 9)
	st.ClearDirty(ctx, 154587, 1, 1)

	rep, err := sync.Pull(ctx)
	if err != nil || rep.Applied != 0 || rep.Matched != 1 {
		t.Fatalf("report=%+v err=%v", rep, err)
	}
	if e, _ := st.ListEntry(ctx, 154587); e.Progress != 9 {
		t.Fatalf("local progress rolled back to %d", e.Progress)
	}
	sync.Run(ctx)
	pushed := up.pushed()
	if len(pushed) != 1 || pushed[0].Get("num_watched_episodes") != "9" {
		t.Fatalf("pushed = %v, want MAL corrected to 9", pushed)
	}
}

// A MAL-only title (negative id, known to the corpus alone) gets a local entry
// from the pull, syncs back to MAL, and is never offered to AniList.
func TestMALPullReachesMALOnlyAnime(t *testing.T) {
	up := &malServer{list: `{"data":[
	  {"node":{"id":424242,"title":"MAL only","num_episodes":12},
	   "list_status":{"status":"plan_to_watch","num_episodes_watched":0,
	                  "updated_at":"2026-08-01T00:00:00+00:00"}}],"paging":{}}`}
	sync, st := newMALSync(t, up)
	ctx := context.Background()
	// The corpus keys a MAL-only title on the negated MAL id.
	if _, err := st.SaveCorpus(ctx, []store.CorpusEntry{{AniListID: -424242, MalID: 424242,
		Titles: []store.CorpusTitle{{Text: "MAL only", Kind: "primary"}}}}); err != nil {
		t.Fatal(err)
	}

	rep, err := sync.Pull(ctx)
	if err != nil || rep.Matched != 1 || rep.Applied != 1 {
		t.Fatalf("report=%+v err=%v", rep, err)
	}
	if e, _ := st.ListEntry(ctx, -424242); e.Status != "PLANNING" {
		t.Fatalf("entry = %+v", e)
	}
	if dirty, _ := st.DirtyEntries(ctx, 0); len(dirty) != 0 {
		t.Fatalf("offered to AniList: %+v", dirty)
	}

	// Watching it locally reaches MAL through the corpus's mal_id.
	if _, err := st.MarkWatched(ctx, -424242, 3); err != nil {
		t.Fatal(err)
	}
	if _, err := sync.Run(ctx); err != nil {
		t.Fatal(err)
	}
	pushed := up.pushed()
	if len(pushed) != 1 || pushed[0].Get("num_watched_episodes") != "3" {
		t.Fatalf("pushed = %v", pushed)
	}
}
