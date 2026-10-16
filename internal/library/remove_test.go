package library

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"kuro/internal/anilist"
	"kuro/internal/mal"
	"kuro/internal/store"
)

// trackers is one fake for both: AniList's GraphQL and MAL's REST, recording what each was asked.
type trackers struct {
	mu        sync.Mutex
	asked     []string
	alFails   bool
	malStatus int
}

func (f *trackers) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Method == http.MethodDelete {
		f.asked = append(f.asked, "mal "+r.URL.Path)
		if f.malStatus != 0 {
			w.WriteHeader(f.malStatus)
			io.WriteString(w, `{"error":"not_found"}`)
			return
		}
		io.WriteString(w, `{}`)
		return
	}
	body, _ := io.ReadAll(r.Body)
	if strings.Contains(string(body), "DeleteMediaListEntry") {
		f.asked = append(f.asked, "anilist delete "+string(body[strings.Index(string(body), `"id":`):]))
		if f.alFails {
			io.WriteString(w, `{"errors":[{"message":"down","status":500}]}`)
			return
		}
		io.WriteString(w, `{"data":{"DeleteMediaListEntry":{"deleted":true}}}`)
		return
	}
	io.WriteString(w, `{"data":{}}`)
}

func (f *trackers) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.asked...)
}

func removeSetup(t *testing.T, f *trackers) (*Sync, *store.Store) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(srv.Close)

	st := openStore(t)
	malID, status, episodes := 77, "CURRENT", 12
	if _, err := st.ImportList(context.Background(),
		[]store.Anime{{ID: 1, MalID: &malID, Romaji: "Show", Episodes: &episodes, Synonyms: "[]", Genres: "[]"}},
		[]store.Entry{{ID: 555, AnimeID: 1, Status: &status, UpdatedAt: 1000}}, store.ImportMerge); err != nil {
		t.Fatal(err)
	}

	al := anilist.New(discard(), anilist.WithEndpoint(srv.URL), anilist.WithoutRateLimit())
	al.SetToken("tok")
	mc := mal.New(discard(), mal.WithAPI(srv.URL), mal.WithCredentials("id", ""), mal.WithoutRateLimit())
	mc.SetToken(mal.Token{Access: "acc", Expires: time.Now().Add(time.Hour)})
	return NewSync(st, al, discard()).WithMAL(NewMALSync(st, mc, discard())), st
}

func onList(t *testing.T, st *store.Store) bool {
	t.Helper()
	e, err := st.ListEntry(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	return e.AnimeID != 0
}

// Removing a show deletes its AniList entry by MediaList id, then MAL's by MAL id, then the row here.
func TestRemoveTakesTheShowOffEveryList(t *testing.T) {
	f := &trackers{}
	s, st := removeSetup(t, f)

	if err := s.Remove(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	got := f.calls()
	if len(got) != 2 || !strings.Contains(got[0], `"id":555`) || got[1] != "mal /anime/77/my_list_status" {
		t.Fatalf("trackers were asked %q, want AniList's entry 555 then MAL's anime 77", got)
	}
	if onList(t, st) {
		t.Error("the show is still on the local list")
	}
}

// A tracker failing leaves the show listed everywhere, so the next sync can't bring it back half-removed.
func TestRemoveKeepsTheShowWhenAniListFails(t *testing.T) {
	f := &trackers{alFails: true}
	s, st := removeSetup(t, f)

	if err := s.Remove(context.Background(), 1); err == nil {
		t.Fatal("want the AniList failure reported")
	}
	for _, c := range f.calls() {
		if strings.HasPrefix(c, "mal") {
			t.Error("MAL was asked after AniList failed")
		}
	}
	if !onList(t, st) {
		t.Error("the show left the local list though AniList still has it")
	}
}

// An entry a tracker no longer has is already where removal wants it.
func TestRemoveTreatsAMissingMALEntryAsDone(t *testing.T) {
	f := &trackers{malStatus: http.StatusNotFound}
	s, st := removeSetup(t, f)

	if err := s.Remove(context.Background(), 1); err != nil {
		t.Fatalf("a 404 from MAL failed the removal: %v", err)
	}
	if onList(t, st) {
		t.Error("the show is still on the local list")
	}
}
