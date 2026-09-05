package library

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"kuro/internal/db"
	"kuro/internal/mal"
	"kuro/internal/store"
)

type malServer struct {
	list string

	mu      sync.Mutex
	patches []url.Values
}

func (m *malServer) handler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch {
			r.ParseForm()
			m.mu.Lock()
			m.patches = append(m.patches, r.PostForm)
			m.mu.Unlock()
			io.WriteString(w, `{}`)
			return
		}
		io.WriteString(w, m.list)
	}
}

func (m *malServer) pushed() []url.Values {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]url.Values(nil), m.patches...)
}

func newMALSync(t *testing.T, upstream *malServer) (*MALSync, *store.Store) {
	t.Helper()

	srv := httptest.NewServer(upstream.handler(t))
	t.Cleanup(srv.Close)

	conn, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := conn.Migrate(); err != nil {
		t.Fatal(err)
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := mal.New(log, mal.WithAPI(srv.URL), mal.WithOAuthBase(srv.URL),
		mal.WithCredentials("id", ""), mal.WithoutRateLimit())
	client.SetToken(mal.Token{Access: "acc", Expires: time.Now().Add(time.Hour)})

	st := store.New(conn)
	return NewMALSync(st, client, log), st
}

func addAnime(t *testing.T, s *store.Store, animeID, malID, episodes int) {
	t.Helper()

	a := store.Anime{ID: animeID, Romaji: "Show", Episodes: &episodes}
	if malID > 0 {
		a.MalID = &malID
	}
	if _, err := s.ImportList(context.Background(), []store.Anime{a}, nil, store.ImportMerge); err != nil {
		t.Fatal(err)
	}
}

func TestMALSyncPushesLocalProgress(t *testing.T) {
	up := &malServer{list: `{"data":[],"paging":{}}`}
	sync, st := newMALSync(t, up)
	ctx := context.Background()

	addAnime(t, st, 154587, 52991, 28)
	st.MarkWatched(ctx, 154587, 10)

	rep, err := sync.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Pushed != 1 || rep.Failed != 0 {
		t.Fatalf("report = %+v", rep)
	}

	pushed := up.pushed()
	if len(pushed) != 1 {
		t.Fatalf("%d PATCH requests", len(pushed))
	}
	if got := pushed[0].Get("num_watched_episodes"); got != "10" {
		t.Errorf("episodes = %q", got)
	}
	if got := pushed[0].Get("status"); got != mal.StatusWatching {
		t.Errorf("status = %q", got)
	}

	// A second run has nothing left to say.
	rep, err = sync.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Pushed != 0 {
		t.Errorf("re-pushed %d entries", rep.Pushed)
	}
	if len(up.pushed()) != 1 {
		t.Errorf("%d total requests, want 1", len(up.pushed()))
	}
}

// The point of importing: MAL already knows most of the list, so connecting an
// account must not fire a request per entry.
func TestImportSuppressesRedundantPushes(t *testing.T) {
	up := &malServer{list: `{"data":[
	  {"node":{"id":52991,"title":"Frieren","num_episodes":28},
	   "list_status":{"status":"watching","num_episodes_watched":10,
	                  "updated_at":"2026-08-01T00:00:00+00:00"}},
	  {"node":{"id":101,"title":"Other","num_episodes":12},
	   "list_status":{"status":"watching","num_episodes_watched":2,
	                  "updated_at":"2026-08-01T00:00:00+00:00"}}],
	  "paging":{}}`}

	sync, st := newMALSync(t, up)
	ctx := context.Background()

	addAnime(t, st, 154587, 52991, 28)
	addAnime(t, st, 1, 101, 12)
	st.MarkWatched(ctx, 154587, 10) // MAL agrees
	st.MarkWatched(ctx, 1, 5)       // MAL is behind

	imp, err := sync.Pull(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if imp.Entries != 2 || imp.Matched != 2 {
		t.Fatalf("import = %+v", imp)
	}

	rep, err := sync.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Pushed != 1 {
		t.Fatalf("pushed %d, want only the entry MAL is behind on", rep.Pushed)
	}
	pushed := up.pushed()
	if len(pushed) != 1 || pushed[0].Get("num_watched_episodes") != "5" {
		t.Errorf("requests = %v", pushed)
	}
}

func TestImportCountsUnmatchedEntries(t *testing.T) {
	up := &malServer{list: `{"data":[
	  {"node":{"id":999999,"title":"Unknown","num_episodes":12},
	   "list_status":{"status":"watching","num_episodes_watched":1,
	                  "updated_at":"2026-08-01T00:00:00+00:00"}}],
	  "paging":{}}`}

	sync, _ := newMALSync(t, up)
	imp, err := sync.Pull(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if imp.Entries != 1 || imp.Matched != 0 || imp.Unmatched != 1 {
		t.Fatalf("import = %+v", imp)
	}
}

func TestPushOneTargetsASingleAnime(t *testing.T) {
	up := &malServer{list: `{"data":[],"paging":{}}`}
	sync, st := newMALSync(t, up)
	ctx := context.Background()

	addAnime(t, st, 1, 101, 12)
	addAnime(t, st, 2, 102, 12)
	st.MarkWatched(ctx, 1, 3)
	st.MarkWatched(ctx, 2, 4)

	if err := sync.PushOne(ctx, 2); err != nil {
		t.Fatal(err)
	}
	pushed := up.pushed()
	if len(pushed) != 1 {
		t.Fatalf("%d requests, want 1", len(pushed))
	}
	if got := pushed[0].Get("num_watched_episodes"); got != "4" {
		t.Errorf("episodes = %q, want anime 2's progress", got)
	}

	// Nothing new to say about it now.
	if err := sync.PushOne(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if len(up.pushed()) != 1 {
		t.Errorf("PushOne repeated a request")
	}
}

func TestSyncIsInertWithoutAConnection(t *testing.T) {
	up := &malServer{list: `{"data":[],"paging":{}}`}
	sync, st := newMALSync(t, up)
	ctx := context.Background()

	sync.mal.SetToken(mal.Token{})
	addAnime(t, st, 1, 101, 12)
	st.MarkWatched(ctx, 1, 3)

	rep, err := sync.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Pushed != 0 || len(up.pushed()) != 0 {
		t.Fatalf("report = %+v, requests = %d", rep, len(up.pushed()))
	}
	if err := sync.PushOne(ctx, 1); err != nil {
		t.Errorf("PushOne without a token should be a no-op, got %v", err)
	}
}

// An anime MAL cannot address must not be retried forever.
func TestUnmappedAnimeIsNeverPushed(t *testing.T) {
	up := &malServer{list: `{"data":[],"paging":{}}`}
	sync, st := newMALSync(t, up)
	ctx := context.Background()

	addAnime(t, st, 7, 0, 12)
	st.MarkWatched(ctx, 7, 3)

	rep, err := sync.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Pushed != 0 || rep.Failed != 0 || len(up.pushed()) != 0 {
		t.Fatalf("report = %+v, requests = %d", rep, len(up.pushed()))
	}
}

// A failure must leave the entry pending rather than recording it as sent.
func TestFailedPushStaysPending(t *testing.T) {
	var fail = true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch && fail {
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, `{"error":"bad_request","message":"nope"}`)
			return
		}
		io.WriteString(w, `{}`)
	}))
	defer srv.Close()

	conn, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.Migrate(); err != nil {
		t.Fatal(err)
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := mal.New(log, mal.WithAPI(srv.URL), mal.WithCredentials("id", ""), mal.WithoutRateLimit())
	client.SetToken(mal.Token{Access: "acc", Expires: time.Now().Add(time.Hour)})

	st := store.New(conn)
	sync := NewMALSync(st, client, log)
	ctx := context.Background()

	addAnime(t, st, 1, 101, 12)
	st.MarkWatched(ctx, 1, 3)

	rep, _ := sync.Run(ctx)
	if rep.Failed != 1 || rep.Pushed != 0 {
		t.Fatalf("report = %+v", rep)
	}

	fail = false
	rep, err = sync.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Pushed != 1 {
		t.Fatalf("retry pushed %d, want the entry to still be pending", rep.Pushed)
	}
}
