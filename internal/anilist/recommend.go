package anilist

import "context"

// AniList recommendations are submitted and voted on by users, so a negative
// rating means the community disagreed with the suggestion.
const recommendQuery = `query Recommend($id: Int!, $perPage: Int!) {
  Media(id: $id, type: ANIME) {
    genres
    recommendations(sort: RATING_DESC, perPage: $perPage) {
      nodes {
        rating
        mediaRecommendation {` + mediaFields + `}
      }
    }
  }
}`

type Recommendation struct {
	Rating int   `json:"rating"`
	Media  Media `json:"media"`
}

type Recommendations struct {
	Genres []string         `json:"genres"`
	Items  []Recommendation `json:"items"`
}

// Recommend returns what the community says is similar. Entries the community
// voted down are dropped: a negative score means "this is not like that".
func (c *Client) Recommend(ctx context.Context, animeID, limit int) (Recommendations, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}

	var out struct {
		Media struct {
			Genres          []string `json:"genres"`
			Recommendations struct {
				Nodes []struct {
					Rating int    `json:"rating"`
					Media  *Media `json:"mediaRecommendation"`
				} `json:"nodes"`
			} `json:"recommendations"`
		} `json:"Media"`
	}
	if err := c.Query(ctx, recommendQuery, map[string]any{"id": animeID, "perPage": limit}, &out); err != nil {
		return Recommendations{}, err
	}

	rec := Recommendations{Genres: out.Media.Genres}
	for _, n := range out.Media.Recommendations.Nodes {
		// A deleted or manga recommendation comes back as null.
		if n.Media == nil || n.Rating < 0 {
			continue
		}
		rec.Items = append(rec.Items, Recommendation{Rating: n.Rating, Media: *n.Media})
	}
	return rec, nil
}
