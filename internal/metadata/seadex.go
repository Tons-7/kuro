package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// SeaDex is a human-curated index of the best release of a given anime. Keyed
// by AniList id, so no title matching is involved.
const SeaDexURL = "https://releases.moe/api/collections/entries/records"

type SeaDexRelease struct {
	AniListID    int
	InfoHash     string
	ReleaseGroup string
	Tracker      string
	URL          string
	IsBest       bool
	DualAudio    bool
	Incomplete   bool
	Notes        string
	Tags         []string
}

type seadexPage struct {
	Page       int `json:"page"`
	PerPage    int `json:"perPage"`
	TotalItems int `json:"totalItems"`
	TotalPages int `json:"totalPages"`
	Items      []struct {
		AniListID  int    `json:"alID"`
		Incomplete bool   `json:"incomplete"`
		Notes      string `json:"notes"`
		Expand     struct {
			Torrents []struct {
				InfoHash     string `json:"infoHash"`
				ReleaseGroup string `json:"releaseGroup"`
				Tracker      string `json:"tracker"`
				URL          string `json:"url"`
				IsBest       bool   `json:"isBest"`
				DualAudio    bool   `json:"dualAudio"`
				Tags         any    `json:"tags"`
			} `json:"trs"`
		} `json:"expand"`
	} `json:"items"`
}

// SeaDex mirrors the whole database. At 500 per page the 2,800-odd entries
// take six requests, after which every lookup is local and free.
func (c *Client) SeaDex(ctx context.Context) ([]SeaDexRelease, error) {
	const perPage = 500

	var out []SeaDexRelease
	for page := 1; page <= 20; page++ {
		params := url.Values{
			"page":    {strconv.Itoa(page)},
			"perPage": {strconv.Itoa(perPage)},
			"expand":  {"trs"},
		}

		body, err := c.getStrict(ctx, c.url("seadex", SeaDexURL)+"?"+params.Encode())
		if err != nil {
			return out, err
		}

		var res seadexPage
		if err := json.Unmarshal(body, &res); err != nil {
			return out, fmt.Errorf("seadex: %w", err)
		}
		if len(res.Items) == 0 {
			break
		}

		for _, item := range res.Items {
			if item.AniListID == 0 {
				continue
			}
			for _, tr := range item.Expand.Torrents {
				hash := usableHash(tr.InfoHash)
				// Private trackers publish the literal "<redacted>" rather than
				// omitting the field; stored as-is it collides across entries.
				if hash == "" && tr.ReleaseGroup == "" {
					continue
				}
				out = append(out, SeaDexRelease{
					AniListID:    item.AniListID,
					InfoHash:     hash,
					ReleaseGroup: strings.TrimSpace(tr.ReleaseGroup),
					Tracker:      tr.Tracker,
					URL:          tr.URL,
					IsBest:       tr.IsBest,
					DualAudio:    tr.DualAudio,
					Incomplete:   item.Incomplete,
					Notes:        item.Notes,
					Tags:         toTags(tr.Tags),
				})
			}
		}

		if res.TotalPages > 0 && page >= res.TotalPages {
			break
		}
	}

	if len(out) == 0 {
		return nil, fmt.Errorf("seadex returned no releases")
	}
	return out, nil
}

// A BitTorrent v1 infohash is 40 hex characters. Anything else — most often
// the placeholder private trackers publish — is not a hash.
func usableHash(raw string) string {
	h := strings.ToLower(strings.TrimSpace(raw))
	if len(h) != 40 {
		return ""
	}
	for _, r := range h {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return ""
		}
	}
	return h
}

// tags arrive as a list on some records and a string on others.
func toTags(v any) []string {
	switch t := v.(type) {
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	case string:
		if t == "" {
			return nil
		}
		return strings.Split(t, ",")
	}
	return nil
}
