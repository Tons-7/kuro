package library

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"kuro/internal/store"
)

func malEntry(malID, watched, score int, status string, updated time.Time) string {
	return fmt.Sprintf(`{"node":{"id":%d,"title":"Show","num_episodes":24},"list_status":{"status":%q,"score":%d,`+
		`"num_episodes_watched":%d,"updated_at":%q}}`, malID, status, score, watched, updated.Format(time.RFC3339))
}

func malList(entries ...string) string {
	out := `{"data":[`
	for i, e := range entries {
		if i > 0 {
			out += ","
		}
		out += e
	}
	return out + `],"paging":{}}`
}

func pull(t *testing.T, sync *MALSync) MALImport {
	t.Helper()
	imp, err := sync.Pull(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return imp
}

// Without AniList nothing clears the dirty flag, which used to block every later site edit.
func TestSiteEditsKeepLandingWithoutAniList(t *testing.T) {
	up := &malServer{list: malList(malEntry(101, 4, 0, "watching", time.Now()))}
	sync, st := newMALSync(t, up)
	ctx := context.Background()
	addAnime(t, st, 1, 101, 24)

	pull(t, sync)
	if e := malRow(t, st, 1); e.Progress != 4 || !e.Dirty {
		t.Fatalf("entry = %+v, want MAL's 4, dirty for AniList", e)
	}

	up.setList(malList(malEntry(101, 8, 7, "watching", time.Now())))
	if imp := pull(t, sync); imp.Applied != 1 {
		t.Fatalf("import = %+v, want the site edit applied", imp)
	}
	if e := malRow(t, st, 1); e.Progress != 8 || e.Score != 70 {
		t.Fatalf("entry = %+v, want the site's 8 and 7/10", e)
	}

	// Watching the next episode here moves MAL forward, never back to the stale 4.
	if _, err := st.MarkWatched(ctx, 1, 9); err != nil {
		t.Fatal(err)
	}
	if _, err := sync.Run(ctx); err != nil {
		t.Fatal(err)
	}
	pushed := up.pushed()
	if len(pushed) != 1 || pushed[0].Get("num_watched_episodes") != "9" {
		t.Fatalf("pushes = %v, want only episode 9", pushed)
	}
}

// Both sides moved since the last push: the local edit wins and goes to MAL.
func TestLocalEditBeatsASiteEditMadeMeanwhile(t *testing.T) {
	up := &malServer{list: malList(malEntry(101, 4, 0, "watching", time.Now()))}
	sync, st := newMALSync(t, up)
	ctx := context.Background()
	addAnime(t, st, 1, 101, 24)
	pull(t, sync)

	st.MarkWatched(ctx, 1, 6)
	up.setList(malList(malEntry(101, 5, 0, "watching", time.Now())))
	if imp := pull(t, sync); imp.Applied != 0 {
		t.Fatalf("import = %+v, the site edit must not win", imp)
	}
	if e := malRow(t, st, 1); e.Progress != 6 {
		t.Fatalf("progress = %d, want the local 6", e.Progress)
	}
	sync.Run(ctx)
	if pushed := up.pushed(); len(pushed) != 1 || pushed[0].Get("num_watched_episodes") != "6" {
		t.Fatalf("pushes = %v", pushed)
	}
}

// Connecting MAL to a list kuro already holds merges: MAL's score stays, the newer side's progress wins.
func TestFirstConnectMergesInsteadOfWiping(t *testing.T) {
	t.Run("MAL edited last", func(t *testing.T) {
		up := &malServer{list: malList(malEntry(101, 24, 8, "completed", time.Now().Add(time.Hour)))}
		sync, st := newMALSync(t, up)
		ctx := context.Background()
		addAnime(t, st, 1, 101, 24)
		st.MarkWatched(ctx, 1, 3)

		pull(t, sync)
		if e := malRow(t, st, 1); e.Progress != 24 || e.Status != "COMPLETED" || e.Score != 80 {
			t.Fatalf("entry = %+v, want MAL's newer progress and its score", e)
		}
		sync.Run(ctx)
		if pushed := up.pushed(); len(pushed) != 0 {
			t.Fatalf("pushes = %v; MAL already holds all of it", pushed)
		}
	})

	t.Run("kuro edited last", func(t *testing.T) {
		up := &malServer{list: malList(malEntry(101, 2, 8, "watching", time.Now().Add(-48*time.Hour)))}
		sync, st := newMALSync(t, up)
		ctx := context.Background()
		addAnime(t, st, 1, 101, 24)
		st.MarkWatched(ctx, 1, 5)

		pull(t, sync)
		if e := malRow(t, st, 1); e.Progress != 5 || e.Score != 80 {
			t.Fatalf("entry = %+v, want local progress and MAL's score", e)
		}
		sync.Run(ctx)
		pushed := up.pushed()
		if len(pushed) != 1 || pushed[0].Get("num_watched_episodes") != "5" {
			t.Fatalf("pushes = %v, want the newer local progress", pushed)
		}
		if got := pushed[0].Get("score"); got != "8" {
			t.Fatalf("score sent = %q; MAL's rating must survive the connect", got)
		}
	})
}

// A refused token says so: the run fails and Settings asks for a reconnect, until a pull goes through.
func TestRefusedTokenAsksForAReconnect(t *testing.T) {
	up := &malServer{list: malList(), patchStatus: http.StatusUnauthorized}
	sync, st := newMALSync(t, up)
	ctx := context.Background()
	addAnime(t, st, 1, 101, 24)
	addAnime(t, st, 2, 102, 24)
	st.MarkWatched(ctx, 1, 3)
	st.MarkWatched(ctx, 2, 3)

	rep, err := sync.Run(ctx)
	if !errors.Is(err, ErrMALReconnect) || rep.Failed != 1 {
		t.Fatalf("rep = %+v err = %v; want one try then a reconnect error", rep, err)
	}
	if v, _ := st.Setting(ctx, MALAuthErrorSetting); v == "" {
		t.Fatal("the refusal was not recorded")
	}
	if err := sync.PushOne(ctx, 1); !errors.Is(err, ErrMALReconnect) {
		t.Fatalf("PushOne err = %v", err)
	}

	pull(t, sync)
	if v, _ := st.Setting(ctx, MALAuthErrorSetting); v != "" {
		t.Fatal("a pull MAL answered should clear the flag")
	}
}

// A push waits for a pull in flight, so the pull cannot judge the push's own write as a site edit.
func TestPushWaitsForAPullInFlight(t *testing.T) {
	up := &malServer{list: malList(malEntry(101, 5, 0, "watching", time.Now())), listing: make(chan struct{})}
	sync, st := newMALSync(t, up)
	ctx := context.Background()
	addAnime(t, st, 1, 101, 24)

	go func() { <-up.listing; up.listing <- struct{}{} }()
	pull(t, sync)

	pulled := make(chan error)
	go func() { _, err := sync.Pull(ctx); pulled <- err }()
	<-up.listing // the pull is reading the list

	st.MarkWatched(ctx, 1, 6)
	pushed := make(chan error)
	go func() { pushed <- sync.PushOne(ctx, 1) }()
	time.Sleep(100 * time.Millisecond)
	if n := len(up.pushed()); n != 0 {
		t.Fatalf("%d pushes while the pull was in flight", n)
	}

	up.listing <- struct{}{}
	if err := <-pulled; err != nil {
		t.Fatal(err)
	}
	if err := <-pushed; err != nil {
		t.Fatal(err)
	}
	if e := malRow(t, st, 1); e.Progress != 6 {
		t.Fatalf("progress = %d, the pull rolled the local 6 back", e.Progress)
	}
	if p := up.pushed(); len(p) != 1 || p[0].Get("num_watched_episodes") != "6" {
		t.Fatalf("pushes = %v", p)
	}
}

// Two AniList entries on one MAL id: one speaks for it in both directions, so they stop overwriting each other.
func TestSharedMALIDHasOneVoice(t *testing.T) {
	up := &malServer{list: malList()}
	sync, st := newMALSync(t, up)
	ctx := context.Background()
	addAnime(t, st, 1, 101, 24)
	addAnime(t, st, 2, 101, 24)
	st.MarkWatched(ctx, 1, 3)
	st.MarkWatched(ctx, 2, 7)
	st.MarkWatched(ctx, 2, 8) // edited last, a tick after anime 1 even within the same second

	if _, err := sync.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if p := up.pushed(); len(p) != 1 || p[0].Get("num_watched_episodes") != "8" {
		t.Fatalf("pushes = %v, want only the entry edited last", p)
	}

	ids, err := st.AniListIDsForMAL(ctx, []int{101})
	if err != nil || ids[101] != 2 {
		t.Fatalf("mapping = %v err = %v, want the same entry", ids, err)
	}
}

func malRow(t *testing.T, s *store.Store, animeID int) store.DirtyEntry {
	t.Helper()
	e, err := s.ListEntry(context.Background(), animeID)
	if err != nil {
		t.Fatal(err)
	}
	return e
}
