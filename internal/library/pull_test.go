package library

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"kuro/internal/anilist"
	"kuro/internal/db"
	"kuro/internal/store"
)

func TestMALPullAppliesASiteEditAndFlagsItForAniList(t *testing.T) {
	up := &malServer{list: `{"data":[
	  {"node":{"id":52991,"title":"Frieren","num_episodes":28},
	   "list_status":{"status":"watching","num_episodes_watched":10,"score":8,
	                  "updated_at":"2026-08-01T00:00:00+00:00"}}],
	  "paging":{}}`}
	sync, st := newMALSync(t, up)
	ctx := context.Background()

	addAnime(t, st, 154587, 52991, 28)
	st.MarkWatched(ctx, 154587, 6)
	if _, err := sync.Run(ctx); err != nil {
		t.Fatal(err)
	}
	st.ClearDirty(ctx, 154587, 1, 1)

	// The site moved on: 10 watched and rated 8.
	imp, err := sync.Pull(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if imp.Applied != 1 {
		t.Fatalf("pull = %+v", imp)
	}
	e, _ := st.ListEntry(ctx, 154587)
	if e.Progress != 10 || e.Score != 80 {
		t.Fatalf("entry = %+v", e)
	}
	dirty, _ := st.DirtyEntries(ctx, 0)
	if len(dirty) != 1 || dirty[0].Score != 80 {
		t.Fatalf("dirty = %+v, want the site edit queued for AniList", dirty)
	}

	// And MAL is not told its own change back.
	before := len(up.pushed())
	if _, err := sync.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(up.pushed()) != before {
		t.Fatal("a pulled change was pushed back to MAL")
	}
}

func TestMALPullCreatesEntriesKuroLacks(t *testing.T) {
	up := &malServer{list: `{"data":[
	  {"node":{"id":52991,"title":"Frieren","num_episodes":28},
	   "list_status":{"status":"completed","num_episodes_watched":28,"score":9,
	                  "num_times_rewatched":1,"updated_at":"2026-08-01T00:00:00+00:00"}}],
	  "paging":{}}`}
	sync, st := newMALSync(t, up)
	ctx := context.Background()
	addAnime(t, st, 154587, 52991, 28)

	imp, err := sync.Pull(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if imp.Matched != 1 || imp.Applied != 1 {
		t.Fatalf("pull = %+v", imp)
	}
	e, _ := st.ListEntry(ctx, 154587)
	if e.Status != "COMPLETED" || e.Progress != 28 || e.Score != 90 || e.Repeat != 1 {
		t.Fatalf("entry = %+v", e)
	}
	if _, err := sync.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(up.pushed()) != 0 {
		t.Fatal("MAL's own entry was pushed back at it")
	}
}

func TestMALPullLeavesAnUnpushedLocalEditAlone(t *testing.T) {
	up := &malServer{list: `{"data":[
	  {"node":{"id":52991,"title":"Frieren","num_episodes":28},
	   "list_status":{"status":"watching","num_episodes_watched":10,
	                  "updated_at":"2026-08-01T00:00:00+00:00"}}],
	  "paging":{}}`}
	sync, st := newMALSync(t, up)
	ctx := context.Background()

	addAnime(t, st, 154587, 52991, 28)
	st.MarkWatched(ctx, 154587, 6)
	sync.Run(ctx)
	// Site says 10; here it just became 12 and is still unpushed to AniList.
	st.MarkWatched(ctx, 154587, 12)

	imp, err := sync.Pull(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if imp.Applied != 0 {
		t.Fatalf("pull = %+v", imp)
	}
	if e, _ := st.ListEntry(ctx, 154587); e.Progress != 12 {
		t.Fatalf("local edit lost: progress = %d", e.Progress)
	}
	// And the local edit still reaches MAL.
	sync.Run(ctx)
	pushed := up.pushed()
	if got := pushed[len(pushed)-1].Get("num_watched_episodes"); got != "12" {
		t.Fatalf("last push = %v", pushed[len(pushed)-1])
	}
}

func TestMALPushCarriesTheScore(t *testing.T) {
	up := &malServer{list: `{"data":[],"paging":{}}`}
	sync, st := newMALSync(t, up)
	ctx := context.Background()

	addAnime(t, st, 154587, 52991, 28)
	st.MarkWatched(ctx, 154587, 1)
	st.SetScore(ctx, 154587, 85)
	if _, err := sync.Run(ctx); err != nil {
		t.Fatal(err)
	}
	pushed := up.pushed()
	if len(pushed) != 1 || pushed[0].Get("score") != "9" {
		t.Fatalf("pushed = %v, want score 9 (85/100 rounded)", pushed)
	}

	// Changing only the score is a change.
	st.SetScore(ctx, 154587, 40)
	sync.Run(ctx)
	pushed = up.pushed()
	if len(pushed) != 2 || pushed[1].Get("score") != "4" {
		t.Fatalf("pushed = %v", pushed)
	}
}

// aniServer fakes enough of AniList for a push then a pull: it records
// mutations and serves one fixed list.
type aniServer struct {
	list string

	mu         sync.Mutex
	saves      []map[string]any
	favourites []int
	toggled    []int
}

func (a *aniServer) setFavourites(ids ...int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.favourites = ids
}

func (a *aniServer) toggles() []int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]int{}, a.toggled...)
}

func (a *aniServer) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		decodeJSON(r, &req)
		if strings.Contains(req.Query, "SaveMediaListEntry") {
			a.mu.Lock()
			a.saves = append(a.saves, req.Variables)
			a.mu.Unlock()
			io.WriteString(w, `{"data":{"SaveMediaListEntry":{"id":77,"mediaId":100,"progress":3,"updatedAt":1000}}}`)
			return
		}
		if strings.Contains(req.Query, "ToggleFavourite") {
			id, _ := req.Variables["animeId"].(float64)
			a.mu.Lock()
			a.toggled = append(a.toggled, int(id))
			// The site flips it, so what it holds changes with the call.
			if i := slices.Index(a.favourites, int(id)); i >= 0 {
				a.favourites = slices.Delete(a.favourites, i, i+1)
			} else {
				a.favourites = append(a.favourites, int(id))
			}
			a.mu.Unlock()
			io.WriteString(w, `{"data":{"ToggleFavourite":{"anime":{"pageInfo":{"total":1}}}}}`)
			return
		}
		// By operation name: the media fragment in the list query asks for a
		// "favourites" count of its own.
		if strings.Contains(req.Query, "query Favourites(") {
			a.mu.Lock()
			nodes := make([]string, 0, len(a.favourites))
			for _, id := range a.favourites {
				nodes = append(nodes, fmt.Sprintf(`{"id":%d}`, id))
			}
			a.mu.Unlock()
			fmt.Fprintf(w, `{"data":{"User":{"favourites":{"anime":{"pageInfo":{"hasNextPage":false},"nodes":[%s]}}}}}`,
				strings.Join(nodes, ","))
			return
		}
		io.WriteString(w, a.list)
	}
}

func decodeJSON(r *http.Request, v any) {
	json.NewDecoder(r.Body).Decode(v)
}

func newAniSync(t *testing.T, up *aniServer) (*Sync, *store.Store) {
	t.Helper()
	srv := httptest.NewServer(up.handler())
	t.Cleanup(srv.Close)

	conn, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := conn.Migrate(); err != nil {
		t.Fatal(err)
	}
	st := store.New(conn)
	st.SetSetting(context.Background(), "anilist.user_id", "1")

	al := anilist.New(discard(), anilist.WithEndpoint(srv.URL), anilist.WithoutRateLimit())
	al.SetToken("tok")
	return NewSync(st, al, discard()).WithImporter(NewImporter(st, al, discard())), st
}

func TestAniListRunPushesThenPullsSiteEdits(t *testing.T) {
	up := &aniServer{list: `{"data":{"MediaListCollection":{"lists":[
      {"name":"Watching","entries":[
        {"id":77,"mediaId":100,"progress":9,"status":"CURRENT","score":80,"updatedAt":2000,
         "media":{"id":100,"title":{"romaji":"Frieren"},"episodes":28}},
        {"id":78,"mediaId":200,"progress":1,"status":"PLANNING","updatedAt":2000,
         "media":{"id":200,"title":{"romaji":"Other"},"episodes":12}}]}]}}}`}
	sync, st := newAniSync(t, up)
	ctx := context.Background()

	st.ImportList(ctx, []store.Anime{{ID: 100, Romaji: "Frieren"}}, nil, store.ImportMerge)
	st.MarkWatched(ctx, 100, 3)
	st.SetScore(ctx, 100, 85)

	rep, err := sync.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Pushed != 1 || rep.Pulled != 2 {
		t.Fatalf("report = %+v", rep)
	}
	if v := up.saves[0]; v["progress"] != float64(3) || v["scoreRaw"] != float64(85) {
		t.Fatalf("pushed variables = %v", v)
	}

	// The site's copy is newer than the push and replaces it once pushed.
	e, _ := st.ListEntry(ctx, 100)
	if e.Progress != 9 || e.Score != 80 || e.ID != 77 {
		t.Fatalf("entry = %+v", e)
	}
	if e, _ := st.ListEntry(ctx, 200); e.AnimeID != 200 || e.Status != "PLANNING" {
		t.Fatalf("site-only entry not pulled: %+v", e)
	}
	if dirty, _ := st.DirtyEntries(ctx, 0); len(dirty) != 0 {
		t.Fatalf("pulled rows must not be re-pushed: %+v", dirty)
	}
}

func TestAniListPullSkipsAnUnpushedEdit(t *testing.T) {
	up := &aniServer{list: `{"data":{"MediaListCollection":{"lists":[
      {"name":"Watching","entries":[
        {"id":77,"mediaId":100,"progress":9,"status":"CURRENT","updatedAt":2000,
         "media":{"id":100,"title":{"romaji":"Frieren"},"episodes":28}}]}]}}}`}
	sync, st := newAniSync(t, up)
	ctx := context.Background()
	st.ImportList(ctx, []store.Anime{{ID: 100, Romaji: "Frieren"}}, nil, store.ImportMerge)
	st.MarkWatched(ctx, 100, 3)

	// Push refused: the row stays dirty and the pull must leave it.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Query string }
		decodeJSON(r, &req)
		if strings.Contains(req.Query, "SaveMediaListEntry") {
			io.WriteString(w, `{"errors":[{"message":"down","status":500}]}`)
			return
		}
		io.WriteString(w, up.list)
	}))
	defer srv.Close()
	al := anilist.New(discard(), anilist.WithEndpoint(srv.URL), anilist.WithoutRateLimit())
	al.SetToken("tok")
	sync = NewSync(st, al, discard()).WithImporter(NewImporter(st, al, discard()))

	rep, err := sync.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Failed != 1 {
		t.Fatalf("report = %+v", rep)
	}
	if e, _ := st.ListEntry(ctx, 100); e.Progress != 3 {
		t.Fatalf("local edit reverted by pull: %+v", e)
	}
}

func TestPushOneReachesBothTrackers(t *testing.T) {
	ani := &aniServer{list: `{"data":{"MediaListCollection":{"lists":[]}}}`}
	sync, st := newAniSync(t, ani)
	mal := &malServer{list: `{"data":[],"paging":{}}`}
	malSync, _ := newMALSync(t, mal)
	// Same database for both halves.
	malSync.store = st
	sync.WithMAL(malSync)
	ctx := context.Background()

	addAnime(t, st, 100, 52991, 28)
	st.MarkWatched(ctx, 100, 4)
	if err := sync.PushOne(ctx, 100); err != nil {
		t.Fatal(err)
	}
	if len(ani.saves) != 1 || len(mal.pushed()) != 1 {
		t.Fatalf("anilist=%d mal=%d pushes", len(ani.saves), len(mal.pushed()))
	}
}
