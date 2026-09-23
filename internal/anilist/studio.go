package anilist

import (
	"context"
	"strings"
)

// The media query has no studio filter; a studio's works come from the studio.
const studioMediaQuery = `query StudioMedia($id: Int!, $page: Int!, $perPage: Int!, $sort: [MediaSort]) {
  Studio(id: $id) {
    name
    media(page: $page, perPage: $perPage, sort: $sort, isMain: true) {
      pageInfo { total hasNextPage }
      nodes {` + mediaFields + `  type }
    }
  }
}`

// StudioMedia is one page of what a studio made as main studio, anime only.
func (c *Client) StudioMedia(ctx context.Context, id int, sort string, page, perPage int) (string, DiscoverPage, error) {
	order := browseSorts[strings.ToLower(sort)]
	if order == "" {
		order = "POPULARITY_DESC"
	}
	var out struct {
		Studio struct {
			Name  string `json:"name"`
			Media struct {
				PageInfo struct {
					Total       int  `json:"total"`
					HasNextPage bool `json:"hasNextPage"`
				} `json:"pageInfo"`
				Nodes []struct {
					Media
					Type string `json:"type"`
				} `json:"nodes"`
			} `json:"media"`
		} `json:"Studio"`
	}
	if err := c.Query(ctx, studioMediaQuery, map[string]any{
		"id": id, "page": max(page, 1), "perPage": clamp(perPage, 30, 50), "sort": []string{order},
	}, &out); err != nil {
		return "", DiscoverPage{}, err
	}
	media := make([]Media, 0, len(out.Studio.Media.Nodes))
	for _, n := range out.Studio.Media.Nodes {
		// Studios also animate manga adaptations listed as manga entries.
		if n.Type == "" || n.Type == "ANIME" {
			media = append(media, n.Media)
		}
	}
	return out.Studio.Name, DiscoverPage{
		Media:       media,
		HasNextPage: out.Studio.Media.PageInfo.HasNextPage,
		Total:       out.Studio.Media.PageInfo.Total,
	}, nil
}

type Studio struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

const studioSearchQuery = `query Studios($search: String) {
  Page(perPage: 10) { studios(search: $search, sort: FAVOURITES_DESC) { id name isAnimationStudio } }
}`

// SearchStudios finds animation studios by name, for the browse filter.
func (c *Client) SearchStudios(ctx context.Context, search string) ([]Studio, error) {
	var out struct {
		Page struct {
			Studios []struct {
				Studio
				Animation bool `json:"isAnimationStudio"`
			} `json:"studios"`
		} `json:"Page"`
	}
	if err := c.Query(ctx, studioSearchQuery, map[string]any{"search": search}, &out); err != nil {
		return nil, err
	}
	studios := make([]Studio, 0, len(out.Page.Studios))
	for _, s := range out.Page.Studios {
		if s.Animation {
			studios = append(studios, s.Studio)
		}
	}
	return studios, nil
}
