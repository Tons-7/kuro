package anilist

import (
	"context"
	"errors"
	"fmt"
)

type FuzzyDate struct {
	Year  *int `json:"year"`
	Month *int `json:"month"`
	Day   *int `json:"day"`
}

func (d FuzzyDate) String() string {
	if d.Year == nil {
		return ""
	}
	y, m, day := *d.Year, 0, 0
	if d.Month != nil {
		m = *d.Month
	}
	if d.Day != nil {
		day = *d.Day
	}
	return fmt.Sprintf("%04d-%02d-%02d", y, m, day)
}

type ListEntry struct {
	ID          int       `json:"id"`
	MediaID     int       `json:"mediaId"`
	Status      *string   `json:"status"`
	Score       int       `json:"score"`
	Progress    int       `json:"progress"`
	Repeat      int       `json:"repeat"`
	Notes       *string   `json:"notes"`
	Private     bool      `json:"private"`
	Hidden      bool      `json:"hiddenFromStatusLists"`
	StartedAt   FuzzyDate `json:"startedAt"`
	CompletedAt FuzzyDate `json:"completedAt"`
	UpdatedAt   int       `json:"updatedAt"`
	Media       Media     `json:"media"`
}

// An explicit score format: a bare `score` inherits an earlier aliased score's format.
const listQuery = `query List($userId: Int!) {
  MediaListCollection(userId: $userId, type: ANIME) {
    hasNextChunk
    lists {
      name status isCustomList
      entries {
        id mediaId status progress repeat notes private hiddenFromStatusLists
        score(format: POINT_100)
        startedAt { year month day }
        completedAt { year month day }
        updatedAt
        media {` + mediaFields + `}
      }
    }
  }
}`

// List returns one entry per anime. AniList repeats an entry once per custom
// list it belongs to, so the same MediaList.id arriving twice is expected.
func (c *Client) List(ctx context.Context, userID int) ([]ListEntry, error) {
	var out struct {
		MediaListCollection struct {
			Lists []struct {
				Entries []ListEntry `json:"entries"`
			} `json:"lists"`
		} `json:"MediaListCollection"`
	}
	// Fresh: a cached list from before our last push reads as undoing it.
	if err := c.QueryFresh(ctx, listQuery, map[string]any{"userId": userID}, &out); err != nil {
		return nil, err
	}

	seen := make(map[int]struct{})
	var entries []ListEntry
	for _, list := range out.MediaListCollection.Lists {
		for _, e := range list.Entries {
			if _, dup := seen[e.ID]; dup {
				continue
			}
			seen[e.ID] = struct{}{}
			entries = append(entries, e)
		}
	}
	return entries, nil
}

const saveQuery = `mutation Save($mediaId: Int!, $progress: Int, $status: MediaListStatus, $completedAt: FuzzyDateInput, $repeat: Int, $scoreRaw: Int) {
  SaveMediaListEntry(mediaId: $mediaId, progress: $progress, status: $status, completedAt: $completedAt, repeat: $repeat, scoreRaw: $scoreRaw) {
    id mediaId status progress repeat updatedAt
  }
}`

// Only set fields go, and score (0-100) and repeat only when non-zero, so a local row can't wipe the site's.
func (c *Client) SetProgress(ctx context.Context, mediaID, progress int, status string, completed *FuzzyDate, repeat, score int, clearScore bool) (ListEntry, error) {
	vars := map[string]any{"mediaId": mediaID, "progress": progress}
	if repeat > 0 {
		vars["repeat"] = repeat
	}
	if score > 0 || clearScore {
		vars["scoreRaw"] = score
	}
	if status != "" {
		vars["status"] = status
	}
	if completed != nil {
		vars["completedAt"] = map[string]any{
			"year": completed.Year, "month": completed.Month, "day": completed.Day,
		}
	}

	var out struct {
		SaveMediaListEntry ListEntry `json:"SaveMediaListEntry"`
	}
	err := c.Query(ctx, saveQuery, vars, &out)
	return out.SaveMediaListEntry, err
}

const deleteQuery = `mutation Delete($id: Int!) {
  DeleteMediaListEntry(id: $id) { deleted }
}`

// DeleteEntry takes a MediaList entry off the account; one already gone (404) counts as done.
func (c *Client) DeleteEntry(ctx context.Context, entryID int) error {
	var out struct {
		DeleteMediaListEntry struct {
			Deleted bool `json:"deleted"`
		} `json:"DeleteMediaListEntry"`
	}
	err := c.Query(ctx, deleteQuery, map[string]any{"id": entryID}, &out)
	var errs Errors
	if errors.As(err, &errs) && errs.NotFound() {
		return nil
	}
	return err
}
