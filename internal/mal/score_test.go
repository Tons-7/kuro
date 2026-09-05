package mal

import (
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestMALScoreRoundsToTen(t *testing.T) {
	for in, want := range map[int]int{0: 0, 4: 0, 5: 1, 85: 9, 84: 8, 100: 10, 120: 10} {
		if got := MALScore(in); got != want {
			t.Errorf("MALScore(%d) = %d, want %d", in, got, want)
		}
	}
	if (Entry{Score: 7}).LocalScore() != 70 {
		t.Fatal("LocalScore should scale 0-10 up to 0-100")
	}
}

func TestSetProgressSendsScoreOnlyWhenRated(t *testing.T) {
	var form url.Values
	c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		form = r.PostForm
		io.WriteString(w, `{}`)
	})
	c.SetToken(Token{Access: "acc", Expires: time.Now().Add(time.Hour)})

	if err := c.SetProgress(t.Context(), 1, 3, "CURRENT", 0, 0); err != nil {
		t.Fatal(err)
	}
	if _, present := form["score"]; present {
		t.Fatal("an unrated entry sent a score, which would clear one on the site")
	}
	if err := c.SetProgress(t.Context(), 1, 3, "CURRENT", 0, 85); err != nil {
		t.Fatal(err)
	}
	if form.Get("score") != "9" {
		t.Fatalf("score = %q, want 9", form.Get("score"))
	}
}

func TestListReadsScoreAndRewatchCount(t *testing.T) {
	c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("fields") == "" {
			t.Error("fields not requested")
		}
		io.WriteString(w, `{"data":[{"node":{"id":1,"title":"A","num_episodes":12},
		  "list_status":{"status":"watching","score":8,"num_episodes_watched":3,
		                 "is_rewatching":true,"num_times_rewatched":2,
		                 "updated_at":"2026-08-01T00:00:00+00:00"}}],"paging":{}}`)
	})
	c.SetToken(Token{Access: "acc", Expires: time.Now().Add(time.Hour)})

	list, err := c.List(t.Context())
	if err != nil || len(list) != 1 {
		t.Fatalf("list=%v err=%v", list, err)
	}
	e := list[0]
	if e.Score != 8 || e.Rewatched != 2 || e.ListStatus() != "REPEATING" {
		t.Fatalf("entry = %+v", e)
	}
}
