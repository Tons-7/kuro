package anilist

import "context"

const favouritesQuery = `query Favourites($userId: Int!, $page: Int!) {
  User(id: $userId) {
    favourites {
      anime(page: $page, perPage: 50) {
        pageInfo { hasNextPage }
        nodes { id }
      }
    }
  }
}`

// Favourites returns the anime ids on the user's favourites list.
func (c *Client) Favourites(ctx context.Context, userID int) ([]int, error) {
	var ids []int

	for page := 1; page <= 40; page++ {
		var out struct {
			User struct {
				Favourites struct {
					Anime struct {
						PageInfo struct {
							HasNextPage bool `json:"hasNextPage"`
						} `json:"pageInfo"`
						Nodes []struct {
							ID int `json:"id"`
						} `json:"nodes"`
					} `json:"anime"`
				} `json:"favourites"`
			} `json:"User"`
		}
		vars := map[string]any{"userId": userID, "page": page}
		if err := c.QueryFresh(ctx, favouritesQuery, vars, &out); err != nil {
			return nil, err
		}

		for _, n := range out.User.Favourites.Anime.Nodes {
			ids = append(ids, n.ID)
		}
		if !out.User.Favourites.Anime.PageInfo.HasNextPage {
			break
		}
	}
	return ids, nil
}

const toggleFavouriteQuery = `mutation Favourite($animeId: Int!) {
  ToggleFavourite(animeId: $animeId) { anime { pageInfo { total } } }
}`

// ToggleFavourite flips the flag; AniList offers no way to set it outright, so
// the caller has to know the current state first.
func (c *Client) ToggleFavourite(ctx context.Context, mediaID int) error {
	var out struct{}
	return c.Query(ctx, toggleFavouriteQuery, map[string]any{"animeId": mediaID}, &out)
}
