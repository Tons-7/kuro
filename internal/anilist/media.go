package anilist

import "context"

// Almost every AniList field is nullable (episodes while airing, title.english
// often), so pointers keep "absent" distinct from "zero".
type Media struct {
	ID              int       `json:"id"`
	IDMal           *int      `json:"idMal"`
	Title           Title     `json:"title"`
	Format          *string   `json:"format"`
	Status          *string   `json:"status"`
	Episodes        *int      `json:"episodes"`
	Duration        *int      `json:"duration"`
	Season          *string   `json:"season"`
	SeasonYear      *int      `json:"seasonYear"`
	Description     *string   `json:"description"`
	Source          *string   `json:"source"`
	Genres          []string  `json:"genres"`
	Synonyms        []string  `json:"synonyms"`
	AverageScore    *int      `json:"averageScore"`
	MeanScore       *int      `json:"meanScore"`
	Popularity      *int      `json:"popularity"`
	Favourites      *int      `json:"favourites"`
	CountryOfOrigin *string   `json:"countryOfOrigin"`
	IsAdult         bool      `json:"isAdult"`
	CoverImage      Cover     `json:"coverImage"`
	BannerImage     *string   `json:"bannerImage"`
	StartDate       FuzzyDate `json:"startDate"`
	EndDate         FuzzyDate `json:"endDate"`
	Studios         Studios   `json:"studios"`
	Tags            []Tag     `json:"tags"`
	Links           []Link    `json:"externalLinks"`
	Trailer         *Trailer  `json:"trailer"`
	NextAiring      *Airing   `json:"nextAiringEpisode"`
}

type Studios struct {
	Nodes []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"nodes"`
}

func (s Studios) Names() []string {
	out := make([]string, 0, len(s.Nodes))
	for _, n := range s.Nodes {
		out = append(out, n.Name)
	}
	return out
}

type Tag struct {
	Name           string `json:"name"`
	Rank           int    `json:"rank"`
	GeneralSpoiler bool   `json:"isGeneralSpoiler"`
	MediaSpoiler   bool   `json:"isMediaSpoiler"`
}

type Link struct {
	Site     string  `json:"site"`
	URL      string  `json:"url"`
	Type     *string `json:"type"`
	Language *string `json:"language"`
}

type Trailer struct {
	ID        *string `json:"id"`
	Site      *string `json:"site"`
	Thumbnail *string `json:"thumbnail"`
}

type Title struct {
	Romaji        *string `json:"romaji"`
	English       *string `json:"english"`
	Native        *string `json:"native"`
	UserPreferred *string `json:"userPreferred"`
}

type Cover struct {
	ExtraLarge *string `json:"extraLarge"`
	Large      *string `json:"large"`
	Medium     *string `json:"medium"`
	Color      *string `json:"color"`
}

type Airing struct {
	Episode         int `json:"episode"`
	AiringAt        int `json:"airingAt"`
	TimeUntilAiring int `json:"timeUntilAiring"`
}

type page struct {
	Page struct {
		PageInfo struct {
			HasNextPage bool `json:"hasNextPage"`
		} `json:"pageInfo"`
		Media []Media `json:"media"`
	} `json:"Page"`
}

const mediaFields = `
  id idMal isAdult countryOfOrigin
  title { romaji english native userPreferred }
  format status episodes duration season seasonYear
  averageScore meanScore popularity favourites genres synonyms
  description(asHtml: false)
  source
  coverImage { extraLarge large medium color }
  bannerImage
  startDate { year month day }
  endDate { year month day }
  studios(isMain: true) { nodes { id name } }
  tags { name rank isGeneralSpoiler isMediaSpoiler }
  externalLinks { site url type language }
  trailer { id site thumbnail }
  nextAiringEpisode { episode airingAt timeUntilAiring }
`

const searchQuery = `query Search($q: String!, $perPage: Int!) {
  Page(page: 1, perPage: $perPage) {
    pageInfo { hasNextPage }
    media(search: $q, type: ANIME, sort: SEARCH_MATCH) {` + mediaFields + `}
  }
}`

// Search is fuzzy, not prefix-based, and not self-consistent: "narut" returns
// nothing while "naruto" works. Treat an empty short-query result as "keep typing".
func (c *Client) Search(ctx context.Context, q string, limit int) ([]Media, error) {
	if limit <= 0 || limit > 50 {
		limit = 20 // perPage is silently clamped to 50 server-side.
	}
	var out page
	err := c.Query(ctx, searchQuery, map[string]any{"q": q, "perPage": limit}, &out)
	return out.Page.Media, err
}

const seasonQuery = `query Season($season: MediaSeason!, $year: Int!, $perPage: Int!) {
  Page(page: 1, perPage: $perPage) {
    pageInfo { hasNextPage }
    media(season: $season, seasonYear: $year, type: ANIME, sort: POPULARITY_DESC) {` + mediaFields + `}
  }
}`

// Fetched separately: AniList caps query complexity at 500 nodes, which nesting
// an edge list in a 50-result page exceeds. relationType version 2 is required.
const relationsQuery = `query Relations($ids: [Int]!) {
  Page(page: 1, perPage: 25) {
    media(id_in: $ids, type: ANIME) {
      id
      episodes
      startDate { year month day }
      relations {
        edges {
          relationType(version: 2)
          node { id type format status episodes startDate { year month day } }
        }
      }
    }
  }
}`

type RelatedMedia struct {
	ID        int       `json:"id"`
	Episodes  *int      `json:"episodes"`
	StartDate FuzzyDate `json:"startDate"`
	Relations struct {
		Edges []RelationEdge `json:"edges"`
	} `json:"relations"`
}

type RelationEdge struct {
	Type string `json:"relationType"`
	Node struct {
		ID        int       `json:"id"`
		Type      string    `json:"type"`
		Format    *string   `json:"format"`
		Status    *string   `json:"status"`
		Episodes  *int      `json:"episodes"`
		StartDate FuzzyDate `json:"startDate"`
	} `json:"node"`
}

func (c *Client) Relations(ctx context.Context, ids []int) ([]RelatedMedia, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	if len(ids) > 25 {
		ids = ids[:25]
	}
	var out struct {
		Page struct {
			Media []RelatedMedia `json:"media"`
		} `json:"Page"`
	}
	err := c.Query(ctx, relationsQuery, map[string]any{"ids": ids}, &out)
	return out.Page.Media, err
}

// airingAt is a unix timestamp in UTC; grouping by local day is the caller's
// job. perPage caps at 50 server-side and pageInfo.total is a fixed 5000, so
// paging must follow hasNextPage.
const scheduleQuery = `query Schedule($start: Int!, $end: Int!, $page: Int!) {
  Page(page: $page, perPage: 50) {
    pageInfo { hasNextPage }
    airingSchedules(airingAt_greater: $start, airingAt_lesser: $end, sort: TIME) {
      id episode airingAt timeUntilAiring mediaId
      media {` + mediaFields + `}
    }
  }
}`

type ScheduleEntry struct {
	ID              int   `json:"id"`
	Episode         int   `json:"episode"`
	AiringAt        int   `json:"airingAt"`
	TimeUntilAiring int   `json:"timeUntilAiring"`
	MediaID         int   `json:"mediaId"`
	Media           Media `json:"media"`
}

func (c *Client) Schedule(ctx context.Context, start, end int64) ([]ScheduleEntry, error) {
	var out []ScheduleEntry

	for page := 1; page <= 20; page++ {
		var res struct {
			Page struct {
				PageInfo struct {
					HasNextPage bool `json:"hasNextPage"`
				} `json:"pageInfo"`
				Schedules []ScheduleEntry `json:"airingSchedules"`
			} `json:"Page"`
		}
		err := c.Query(ctx, scheduleQuery, map[string]any{
			"start": start, "end": end, "page": page,
		}, &res)
		if err != nil {
			return out, err
		}

		out = append(out, res.Page.Schedules...)
		if !res.Page.PageInfo.HasNextPage || len(res.Page.Schedules) == 0 {
			break
		}
	}
	return out, nil
}

const byIDsQuery = `query ByIDs($ids: [Int]!) {
  Page(page: 1, perPage: 50) {
    pageInfo { hasNextPage }
    media(id_in: $ids, type: ANIME) {` + mediaFields + `}
  }
}`

// MediaByIDs fetches up to 50 anime per request. A missing id is silently
// omitted rather than nulling the entire response.
func (c *Client) MediaByIDs(ctx context.Context, ids []int) ([]Media, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	if len(ids) > 50 {
		ids = ids[:50]
	}

	var out page
	err := c.Query(ctx, byIDsQuery, map[string]any{"ids": ids}, &out)
	if err != nil || !missingAny(ids, out.Page.Media) {
		return out.Page.Media, err
	}

	// AniList hides adult titles from an account that has not opted into them,
	// and says nothing about it. They still appear in the schedule, so their
	// pages have to open.
	var anon page
	if c.queryAnonymous(ctx, byIDsQuery, map[string]any{"ids": ids}, &anon) == nil &&
		len(anon.Page.Media) > len(out.Page.Media) {
		return anon.Page.Media, nil
	}
	return out.Page.Media, nil
}

// missingAny reports that an id was asked for and not returned.
func missingAny(ids []int, got []Media) bool {
	have := make(map[int]struct{}, len(got))
	for _, m := range got {
		have[m.ID] = struct{}{}
	}
	for _, id := range ids {
		if _, ok := have[id]; !ok {
			return true
		}
	}
	return false
}

// StreamingEpisode is one entry of the streaming-site listing. Titles look like
// "Episode 4 - The Perfect Crimson", so the number is parsed from the string.
type StreamingEpisode struct {
	Title     string `json:"title"`
	Thumbnail string `json:"thumbnail"`
	URL       string `json:"url"`
	Site      string `json:"site"`
}

const streamingQuery = `query Streaming($id: Int!) {
  Media(id: $id, type: ANIME) {
    streamingEpisodes { title thumbnail url site }
  }
}`

// StreamingEpisodes is fetched one show at a time, kept out of mediaFields:
// nesting an unbounded list in a fifty-result page exceeds the complexity limit.
func (c *Client) StreamingEpisodes(ctx context.Context, id int) ([]StreamingEpisode, error) {
	var out struct {
		Media struct {
			StreamingEpisodes []StreamingEpisode `json:"streamingEpisodes"`
		} `json:"Media"`
	}
	if err := c.Query(ctx, streamingQuery, map[string]any{"id": id}, &out); err != nil {
		// An adult title is invisible to an authenticated account, and a
		// missing thumbnail is not worth failing the page over.
		if c.queryAnonymous(ctx, streamingQuery, map[string]any{"id": id}, &out) != nil {
			return nil, err
		}
	}
	return out.Media.StreamingEpisodes, nil
}

func (c *Client) Season(ctx context.Context, season string, year, limit int) ([]Media, error) {
	if limit <= 0 || limit > 50 {
		limit = 50
	}
	var out page
	err := c.Query(ctx, seasonQuery, map[string]any{"season": season, "year": year, "perPage": limit}, &out)
	return out.Page.Media, err
}
