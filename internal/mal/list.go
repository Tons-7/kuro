package mal

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// MAL's statuses, which do not match AniList's.
const (
	StatusWatching    = "watching"
	StatusCompleted   = "completed"
	StatusOnHold      = "on_hold"
	StatusDropped     = "dropped"
	StatusPlanToWatch = "plan_to_watch"
)

var fromAniList = map[string]string{
	"CURRENT":   StatusWatching,
	"REPEATING": StatusWatching,
	"COMPLETED": StatusCompleted,
	"PAUSED":    StatusOnHold,
	"DROPPED":   StatusDropped,
	"PLANNING":  StatusPlanToWatch,
}

var toAniList = map[string]string{
	StatusWatching:    "CURRENT",
	StatusCompleted:   "COMPLETED",
	StatusOnHold:      "PAUSED",
	StatusDropped:     "DROPPED",
	StatusPlanToWatch: "PLANNING",
}

// Status maps a kuro status onto MAL's vocabulary. Rewatching is not a status
// on MAL, only a flag, so it reports as watching.
func Status(status string) string {
	if s, ok := fromAniList[strings.ToUpper(status)]; ok {
		return s
	}
	return ""
}

func StatusToAniList(status string) string {
	return toAniList[strings.ToLower(status)]
}

type Entry struct {
	AnimeID  int
	Title    string
	Episodes int
	Status   string
	Score    int
	Watched  int
	// Rewatching is MAL's flag on a "watching" entry; it has no separate status.
	Rewatching bool
	Rewatched  int
	Updated    time.Time
}

// LocalScore is the entry's score on kuro's 0-100 scale; MAL rates 0-10.
func (e Entry) LocalScore() int { return e.Score * 10 }

// MALScore converts a 0-100 score to MAL's 0-10, rounding to nearest.
func MALScore(score int) int {
	if score <= 0 {
		return 0
	}
	return min(10, (score+5)/10)
}

// ListStatus is the entry's status in AniList's vocabulary, which is what kuro
// stores: a rewatch is REPEATING there, while MAL says watching + is_rewatching.
func (e Entry) ListStatus() string {
	if e.Rewatching && strings.EqualFold(e.Status, StatusWatching) {
		return "REPEATING"
	}
	return StatusToAniList(e.Status)
}

type listPage struct {
	Data []struct {
		Node struct {
			ID          int    `json:"id"`
			Title       string `json:"title"`
			NumEpisodes int    `json:"num_episodes"`
		} `json:"node"`
		ListStatus struct {
			Status string `json:"status"`
			Score  int    `json:"score"`
			// Note the asymmetry: reads use num_episodes_watched, writes use
			// num_watched_episodes. Mixing them up fails silently.
			NumEpisodesWatched int       `json:"num_episodes_watched"`
			IsRewatching       bool      `json:"is_rewatching"`
			NumTimesRewatched  int       `json:"num_times_rewatched"`
			UpdatedAt          time.Time `json:"updated_at"`
		} `json:"list_status"`
	} `json:"data"`
	Paging struct {
		Next string `json:"next"`
	} `json:"paging"`
}

// List pulls the whole authenticated user's anime list, following MAL's paging
// links rather than computing offsets.
func (c *Client) List(ctx context.Context) ([]Entry, error) {
	q := url.Values{
		"fields": {"list_status{status,score,num_episodes_watched,is_rewatching,num_times_rewatched,updated_at},num_episodes"},
		"limit":  {"1000"},
		"nsfw":   {"true"},
	}
	next := c.api + "/users/@me/animelist?" + q.Encode()

	var out []Entry
	for pages := 0; next != "" && pages < 50; pages++ {
		var page listPage
		if err := c.get(ctx, next, &page); err != nil {
			return out, err
		}

		for _, row := range page.Data {
			out = append(out, Entry{
				AnimeID:    row.Node.ID,
				Title:      row.Node.Title,
				Episodes:   row.Node.NumEpisodes,
				Status:     row.ListStatus.Status,
				Score:      row.ListStatus.Score,
				Watched:    row.ListStatus.NumEpisodesWatched,
				Rewatching: row.ListStatus.IsRewatching,
				Rewatched:  row.ListStatus.NumTimesRewatched,
				Updated:    row.ListStatus.UpdatedAt,
			})
		}
		next = page.Paging.Next
	}
	return out, nil
}

// SetProgress updates one entry. Status is optional; an empty string leaves
// whatever MAL already has. MAL has no rewatching status: a rewatch is
// "watching" with is_rewatching set, and finished rewatches are a count — sent
// only once kuro has counted one, so a locally created row's zero cannot wipe
// a count built up on the site. The score, on kuro's 0-100 scale, is treated
// the same way.
func (c *Client) SetProgress(ctx context.Context, animeID, watched int, status string, rewatched, score int) error {
	if animeID <= 0 {
		return fmt.Errorf("mal: invalid anime id %d", animeID)
	}

	form := url.Values{"num_watched_episodes": {strconv.Itoa(watched)}}
	if rewatched > 0 {
		form.Set("num_times_rewatched", strconv.Itoa(rewatched))
	}
	if s := MALScore(score); s > 0 {
		form.Set("score", strconv.Itoa(s))
	}
	if s := Status(status); s != "" {
		form.Set("status", s)
		form.Set("is_rewatching", strconv.FormatBool(status == "REPEATING"))
	}

	endpoint := fmt.Sprintf("%s/anime/%d/my_list_status", c.api, animeID)
	return c.do(ctx, "PATCH", endpoint, form, nil)
}

type viewer struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

func (c *Client) Viewer(ctx context.Context) (viewer, error) {
	var v viewer
	err := c.get(ctx, c.api+"/users/@me", &v)
	return v, err
}
