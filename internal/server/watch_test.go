package server

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"kuro/internal/config"
	"kuro/internal/store"
)

// watchHarness is a server knowing one twelve-episode show, id 1.
func watchHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t, config.Config{}, nil)
	episodes := 12
	if _, err := h.store.ImportList(context.Background(),
		[]store.Anime{{ID: 1, Romaji: "Show", Synonyms: "[]", Genres: "[]", Episodes: &episodes}},
		nil, store.ImportMerge); err != nil {
		t.Fatal(err)
	}
	return h
}

func report(t *testing.T, h *harness, position, played float64) bool {
	t.Helper()
	code, body := h.call(t, "POST", "/api/progress",
		fmt.Sprintf(`{"animeId":1,"episode":1,"position":%v,"duration":1400,"played":%v}`, position, played))
	if code != http.StatusOK || body["saved"] != true {
		t.Fatalf("progress answered %d %v", code, body)
	}
	return body["watched"].(bool)
}

func progressOf(t *testing.T, h *harness) store.DirtyEntry {
	t.Helper()
	entry, err := h.store.ListEntry(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	return entry
}

// Reaching the end is not enough: the episode has to have actually played.
func TestProgressCountsAnEpisodeWatchedOnlyOncePlayed(t *testing.T) {
	h := watchHarness(t)

	if report(t, h, 100, 100) {
		t.Fatal("watched two minutes in")
	}
	if report(t, h, 1390, 5) {
		t.Fatal("a jump to the ending counted as watched")
	}
	watched := false
	for i := 0; i < 6 && !watched; i++ {
		watched = report(t, h, 1390, 120)
	}
	if !watched {
		t.Fatal("an episode played through to the end never counted as watched")
	}
	// Rewinding a finished episode does not take the tick back.
	if !report(t, h, 60, 10) {
		t.Error("a watched episode lost its tick on a rewind")
	}
}

func TestProgressRefusesAReportWithoutAnEpisode(t *testing.T) {
	h := watchHarness(t)
	for _, body := range []string{`not json`, `{"animeId":1}`, `{"episode":1}`, `{"animeId":1,"episode":0}`} {
		if code, _ := h.call(t, "POST", "/api/progress", body); code != http.StatusBadRequest {
			t.Errorf("%s answered %d, want 400", body, code)
		}
	}
}

// Marking by hand moves list progress; unmarking rewinds it rather than deleting it.
func TestMarkingWatchedByHandMovesListProgress(t *testing.T) {
	h := watchHarness(t)

	code, body := h.call(t, "POST", "/api/watched", `{"animeId":1,"episode":3}`)
	if code != http.StatusOK || body["watched"] != true {
		t.Fatalf("answered %d %v", code, body)
	}
	if got := progressOf(t, h).Progress; got != 3 {
		t.Errorf("progress is %d after marking episode 3", got)
	}

	if _, body := h.call(t, "POST", "/api/watched", `{"animeId":1,"episode":3,"watched":false}`); body["watched"] != false {
		t.Fatalf("unmarking answered %v", body)
	}
	if got := progressOf(t, h).Progress; got != 2 {
		t.Errorf("progress is %d after unmarking episode 3, want 2", got)
	}
	if code, _ := h.call(t, "POST", "/api/watched", `{"animeId":1}`); code != http.StatusBadRequest {
		t.Errorf("a mark without an episode answered %d, want 400", code)
	}
}

func TestScoreIsSavedAndBounded(t *testing.T) {
	h := watchHarness(t)

	if code, _ := h.call(t, "POST", "/api/score", `{"animeId":1,"score":80}`); code != http.StatusOK {
		t.Fatalf("answered %d", code)
	}
	if got := progressOf(t, h).Score; got != 80 {
		t.Errorf("score is %d, want 80", got)
	}
	for _, body := range []string{`{"animeId":1,"score":101}`, `{"animeId":1,"score":-1}`, `{"score":50}`} {
		if code, _ := h.call(t, "POST", "/api/score", body); code != http.StatusBadRequest {
			t.Errorf("%s answered %d, want 400", body, code)
		}
	}
	if got := progressOf(t, h).Score; got != 80 {
		t.Errorf("a refused score changed it to %d", got)
	}
}

func TestListStatusIsSavedAndUnknownOnesRefused(t *testing.T) {
	h := watchHarness(t)

	if code, body := h.call(t, "POST", "/api/status", `{"animeId":1,"status":"PAUSED","score":70}`); code != http.StatusOK {
		t.Fatalf("answered %d %v", code, body)
	}
	if e := progressOf(t, h); e.Status != "PAUSED" || e.Score != 70 {
		t.Errorf("entry is %s scored %d, want PAUSED 70", e.Status, e.Score)
	}

	// A status alone leaves the score as it was.
	h.call(t, "POST", "/api/status", `{"animeId":1,"status":"CURRENT"}`)
	if e := progressOf(t, h); e.Status != "CURRENT" || e.Score != 70 {
		t.Errorf("entry is %s scored %d, want CURRENT 70", e.Status, e.Score)
	}

	code, body := h.call(t, "POST", "/api/status", `{"animeId":1,"status":"WATCHING"}`)
	if code != http.StatusBadRequest || body["allowed"] == nil {
		t.Errorf("an unknown status answered %d %v", code, body)
	}
	if code, _ := h.call(t, "POST", "/api/status", `{"animeId":1,"status":"CURRENT","score":140}`); code != http.StatusBadRequest {
		t.Errorf("a score of 140 answered %d, want 400", code)
	}
}

func historyCount(t *testing.T, h *harness) int {
	t.Helper()
	code, body := h.call(t, "GET", "/api/history", "")
	if code != http.StatusOK {
		t.Fatalf("history answered %d %v", code, body)
	}
	return len(body["items"].([]any))
}

func TestHistoryListsWhatPlayedAndForgetsOnRequest(t *testing.T) {
	h := watchHarness(t)
	report(t, h, 100, 100)
	h.call(t, "POST", "/api/progress", `{"animeId":1,"episode":2,"position":50,"duration":1400,"played":50}`)
	if n := historyCount(t, h); n != 2 {
		t.Fatalf("history holds %d entries, want 2", n)
	}

	// Naming nothing must not clear everything.
	if code, _ := h.call(t, "POST", "/api/history/forget", `{}`); code != http.StatusBadRequest || historyCount(t, h) != 2 {
		t.Fatalf("an empty forget answered %d and left %d entries", code, historyCount(t, h))
	}

	if _, body := h.call(t, "POST", "/api/history/forget", `{"animeId":1,"episode":2}`); body["removed"] != float64(1) {
		t.Errorf("forgetting one episode removed %v", body["removed"])
	}
	if n := historyCount(t, h); n != 1 {
		t.Errorf("history holds %d entries after forgetting one", n)
	}
	h.call(t, "POST", "/api/history/forget", `{"all":true}`)
	if n := historyCount(t, h); n != 0 {
		t.Errorf("history holds %d entries after clearing it", n)
	}
}

func TestDismissingTakesAnEpisodeOutOfContinueWatching(t *testing.T) {
	h := watchHarness(t)
	ctx := context.Background()
	report(t, h, 300, 100)
	if at, err := h.store.ResumeAt(ctx, 1, "1"); err != nil || at != 300 {
		t.Fatalf("a half-watched episode resumes at %v, want 300 (err %v)", at, err)
	}

	if code, body := h.call(t, "POST", "/api/continue/dismiss", `{"animeId":1,"episode":1}`); code != http.StatusOK || body["dismissed"] != true {
		t.Fatalf("answered %d %v", code, body)
	}
	page, err := h.store.ContinueWatching(ctx, store.Paging{Page: 1, PerPage: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 0 {
		t.Errorf("a dismissed episode is still offered to continue: %v", page.Items)
	}
	if code, _ := h.call(t, "POST", "/api/continue/dismiss", `{"animeId":1}`); code != http.StatusBadRequest {
		t.Errorf("a dismiss without an episode answered %d, want 400", code)
	}
}

func unread(t *testing.T, h *harness) (items int, count float64) {
	t.Helper()
	code, body := h.call(t, "GET", "/api/notifications", "")
	if code != http.StatusOK {
		t.Fatalf("notifications answered %d %v", code, body)
	}
	return len(body["items"].([]any)), body["unread"].(float64)
}

func TestNotificationsAreReadAndDeletedOneOrAll(t *testing.T) {
	h := watchHarness(t)
	ctx := context.Background()
	var ids []int64
	for episode := 1; episode <= 3; episode++ {
		anime := 1
		id, err := h.store.AddNotification(ctx, store.Notification{
			Kind: store.NotifyRelease, AnimeID: &anime, Episode: &episode, Title: fmt.Sprint("Episode ", episode),
		})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if items, count := unread(t, h); items != 3 || count != 3 {
		t.Fatalf("%d notifications, %v unread; want 3 and 3", items, count)
	}

	h.call(t, "POST", fmt.Sprint("/api/notifications/read?id=", ids[0]), "")
	if _, count := unread(t, h); count != 2 {
		t.Errorf("%v unread after reading one, want 2", count)
	}

	// An id that does not parse must not fall through to "all of them".
	if code, _ := h.call(t, "DELETE", "/api/notifications?id=abc", ""); code != http.StatusBadRequest {
		t.Errorf("a malformed id answered %d, want 400", code)
	}
	if items, _ := unread(t, h); items != 3 {
		t.Fatalf("a malformed delete removed notifications: %d left", items)
	}

	if _, body := h.call(t, "DELETE", fmt.Sprint("/api/notifications?id=", ids[1]), ""); body["removed"] != float64(1) {
		t.Errorf("deleting one removed %v", body["removed"])
	}
	h.call(t, "POST", "/api/notifications/read", "")
	if items, count := unread(t, h); items != 2 || count != 0 {
		t.Errorf("%d notifications, %v unread after reading all; want 2 and 0", items, count)
	}
	h.call(t, "DELETE", "/api/notifications", "")
	if items, _ := unread(t, h); items != 0 {
		t.Errorf("%d notifications left after deleting all", items)
	}
}

func TestFollowNeedsAShow(t *testing.T) {
	h := watchHarness(t)
	if code, body := h.call(t, "POST", "/api/follow", `{"animeId":1,"enabled":true}`); code != http.StatusOK || body["following"] != true {
		t.Errorf("answered %d %v", code, body)
	}
	if code, _ := h.call(t, "POST", "/api/follow", `{"enabled":true}`); code != http.StatusBadRequest {
		t.Errorf("a follow without a show answered %d, want 400", code)
	}
}
