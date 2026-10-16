package update

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"

	"kuro/internal/store"
)

// releaseList serves /releases/latest (newest, no assets needed) and the list, drafts and pre-releases included.
func releaseList(t *testing.T, tags ...string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/"+Repo+"/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"tag_name": "v" + tags[0], "body": "notes " + tags[0]})
	})
	mux.HandleFunc("/repos/"+Repo+"/releases", func(w http.ResponseWriter, r *http.Request) {
		list := []map[string]any{
			{"tag_name": "v9.9.9", "body": "a draft", "draft": true},
			{"tag_name": "v9.9.8", "body": "a beta", "prerelease": true},
		}
		// Out of order on purpose: the list is sorted by version, not trusted.
		for _, tag := range slices.Backward(tags) {
			list = append(list, map[string]any{"tag_name": "v" + tag, "body": "notes " + tag})
		}
		json.NewEncoder(w).Encode(list)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func versions(ns []Notes) []string {
	out := make([]string, len(ns))
	for i, n := range ns {
		out[i] = n.Version
	}
	return out
}

// Several versions behind, every release in between is offered with its notes, not just the newest.
func TestCheckListsEveryPendingRelease(t *testing.T) {
	setVersion(t, "2026.10.13")
	gh := releaseList(t, "2026.10.16", "2026.10.15", "2026.10.13.1", "2026.10.13", "2026.10.12")
	u := newUpdater(t, testStore(t), gh, filepath.Join(t.TempDir(), "kuro.exe"))

	st, err := u.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := versions(st.Pending), []string{"2026.10.16", "2026.10.15", "2026.10.13.1"}; !slices.Equal(got, want) {
		t.Errorf("pending = %v, want %v (no draft, no beta, nothing older)", got, want)
	}
	if st.Pending[0].Notes != "notes 2026.10.16" {
		t.Errorf("notes = %q", st.Pending[0].Notes)
	}
	if got := versions(st.WhatsNew); !slices.Equal(got, []string{"2026.10.13"}) {
		t.Errorf("what's new = %v, want this version's own", got)
	}
}

// A list GitHub won't give still leaves the latest release's own notes.
func TestCheckFallsBackToTheLatestNotes(t *testing.T) {
	setVersion(t, "2026.10.13")
	gh := fakeGitHub(t, "2026.10.16", []byte("new"), true)
	u := newUpdater(t, testStore(t), gh, filepath.Join(t.TempDir(), "kuro.exe"))

	st, err := u.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Pending) != 1 || st.Pending[0].Version != "2026.10.16" || st.Pending[0].Notes != "notes" {
		t.Fatalf("pending = %+v, want the latest release alone", st.Pending)
	}
}

// The first start after an update says so once, and what's new covers every version since the old one.
func TestStartedAfterAnUpdate(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	gh := releaseList(t, "2026.10.16", "2026.10.15", "2026.10.13")

	setVersion(t, "2026.10.13")
	newUpdater(t, st, gh, "kuro.exe").Started(ctx)
	if items, _ := st.Notifications(ctx, false, 10); len(items) != 0 {
		t.Fatalf("a first install announced an update: %+v", items)
	}

	Version = "2026.10.16"
	for range 2 {
		u := newUpdater(t, st, gh, "kuro.exe")
		u.Started(ctx)
		got, err := u.Check(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if got.UpdatedFrom != "2026.10.13" {
			t.Errorf("updated from = %q", got.UpdatedFrom)
		}
		if v := versions(got.WhatsNew); !slices.Equal(v, []string{"2026.10.16", "2026.10.15"}) {
			t.Errorf("what's new = %v, want every version since 2026.10.13", v)
		}
	}
	items, _ := st.Notifications(ctx, false, 10)
	if len(items) != 1 || items[0].Kind != store.NotifyUpdate || items[0].Title != "Updated to kuro 2026.10.16" {
		t.Fatalf("notifications = %+v, want one 'Updated to' on the first start only", items)
	}
}

// Versions before this one recorded nothing: the announcement of this very version marks an update.
func TestStartedAfterAnUpdateFromAnOlderUpdater(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	setVersion(t, "2026.10.16")
	if err := st.SetSetting(ctx, "update.notified", "2026.10.16"); err != nil {
		t.Fatal(err)
	}
	u := newUpdater(t, st, releaseList(t, "2026.10.16", "2026.10.15"), "kuro.exe")
	u.Started(ctx)
	got, err := u.Check(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if v := versions(got.WhatsNew); !slices.Equal(v, []string{"2026.10.16"}) {
		t.Errorf("what's new = %v, want this version's (the old one is unknown)", v)
	}
	if items, _ := st.Notifications(ctx, false, 10); len(items) != 1 {
		t.Fatalf("notifications = %+v, want the update announced", items)
	}
}

// Rolling back to an older binary is not an update to announce.
func TestStartedAfterARollbackSaysNothing(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	if err := st.SetSetting(ctx, "update.version", "2026.10.16"); err != nil {
		t.Fatal(err)
	}
	setVersion(t, "2026.10.15")
	newUpdater(t, st, releaseList(t, "2026.10.16"), "kuro.exe").Started(ctx)
	if items, _ := st.Notifications(ctx, false, 10); len(items) != 0 {
		t.Fatalf("a rollback announced an update: %+v", items)
	}
}
