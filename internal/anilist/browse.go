package anilist

import (
	"context"
	"strings"
)

// Browse is the filter set AniList's media query accepts. Zero values are
// omitted, so an empty Browse is "everything, most popular first".
type Browse struct {
	Search        string
	Genres        []string
	ExcludeGenres []string
	Tags          []string
	Year          int
	Season        string
	Formats       []string
	Statuses      []string
	MinScore      int
	MinEpisodes   int
	MaxEpisodes   int
	Country       string
	Source        string
	OnlyAdult     bool
	Sort          string

	Page    int
	PerPage int
}

// browseSorts are the orderings the API accepts here. Search results ignore
// the requested sort in favour of match quality, which is what a user typing a
// title expects.
var browseSorts = map[string]string{
	"popular":    "POPULARITY_DESC",
	"trending":   "TRENDING_DESC",
	"score":      "SCORE_DESC",
	"favourites": "FAVOURITES_DESC",
	"newest":     "START_DATE_DESC",
	"oldest":     "START_DATE",
	"title":      "TITLE_ROMAJI",
	"episodes":   "EPISODES_DESC",
}

func BrowseSorts() []string {
	out := make([]string, 0, len(browseSorts))
	for k := range browseSorts {
		out = append(out, k)
	}
	return out
}

var (
	Formats  = []string{"TV", "TV_SHORT", "MOVIE", "SPECIAL", "OVA", "ONA", "MUSIC"}
	Statuses = []string{"FINISHED", "RELEASING", "NOT_YET_RELEASED", "CANCELLED", "HIATUS"}
	Seasons  = []string{"WINTER", "SPRING", "SUMMER", "FALL"}
	Sources  = []string{"ORIGINAL", "MANGA", "LIGHT_NOVEL", "VISUAL_NOVEL", "VIDEO_GAME",
		"NOVEL", "DOUJINSHI", "ANIME", "WEB_NOVEL", "LIVE_ACTION", "GAME",
		"COMIC", "MULTIMEDIA_PROJECT", "PICTURE_BOOK", "OTHER"}
)

const browseQuery = `query Browse(
  $search: String, $genres: [String], $excludeGenres: [String], $tags: [String],
  $year: Int, $season: MediaSeason, $formats: [MediaFormat], $statuses: [MediaStatus],
  $minScore: Int, $minEpisodes: Int, $maxEpisodes: Int, $country: CountryCode,
  $source: MediaSource, $isAdult: Boolean, $sort: [MediaSort],
  $page: Int!, $perPage: Int!
) {
  Page(page: $page, perPage: $perPage) {
    pageInfo { total currentPage lastPage hasNextPage }
    media(
      type: ANIME
      search: $search
      genre_in: $genres
      genre_not_in: $excludeGenres
      tag_in: $tags
      seasonYear: $year
      season: $season
      format_in: $formats
      status_in: $statuses
      averageScore_greater: $minScore
      episodes_greater: $minEpisodes
      episodes_lesser: $maxEpisodes
      countryOfOrigin: $country
      source: $source
      isAdult: $isAdult
      sort: $sort
    ) {` + mediaFields + `}
  }
}`

func (c *Client) BrowseMedia(ctx context.Context, b Browse) (DiscoverPage, error) {
	vars := map[string]any{
		"page":    max(b.Page, 1),
		"perPage": clamp(b.PerPage, 30, 50),
		"isAdult": nil,
	}

	// A null variable removes the filter; an empty list matches nothing.
	setString(vars, "search", b.Search)
	setList(vars, "genres", b.Genres)
	setList(vars, "excludeGenres", b.ExcludeGenres)
	setList(vars, "tags", b.Tags)
	setList(vars, "formats", upperAll(b.Formats))
	setList(vars, "statuses", upperAll(b.Statuses))
	setString(vars, "season", strings.ToUpper(b.Season))
	setString(vars, "country", b.Country)
	setString(vars, "source", strings.ToUpper(b.Source))

	if b.Year > 0 {
		vars["year"] = b.Year
	}
	if b.MinScore > 0 {
		vars["minScore"] = b.MinScore
	}
	// episodes_greater is exclusive, so subtract one to make the bound inclusive.
	if b.MinEpisodes > 0 {
		vars["minEpisodes"] = b.MinEpisodes - 1
	}
	if b.MaxEpisodes > 0 {
		vars["maxEpisodes"] = b.MaxEpisodes + 1
	}
	// Every hentai title is adult; excluding adult alongside the genre matched nothing.
	switch {
	case b.OnlyAdult || hasGenre(b.Genres, "Hentai"):
		vars["isAdult"] = true
	default:
		vars["isAdult"] = false
	}

	sort := browseSorts[strings.ToLower(b.Sort)]
	switch {
	case b.Search != "":
		// Relevance beats any requested ordering when there is a query.
		sort = "SEARCH_MATCH"
	case sort == "":
		sort = "POPULARITY_DESC"
	}
	vars["sort"] = []string{sort}

	var out struct {
		Page struct {
			PageInfo struct {
				Total       int  `json:"total"`
				HasNextPage bool `json:"hasNextPage"`
			} `json:"pageInfo"`
			Media []Media `json:"media"`
		} `json:"Page"`
	}
	if err := c.Query(ctx, browseQuery, vars, &out); err != nil {
		return DiscoverPage{}, err
	}
	return DiscoverPage{
		Media:       out.Page.Media,
		HasNextPage: out.Page.PageInfo.HasNextPage,
		Total:       out.Page.PageInfo.Total,
	}, nil
}

const genreQuery = `query { GenreCollection MediaTagCollection { name category isAdult } }`

// TagOption is a tag as it appears in the filter vocabulary, which carries a
// category for grouping rather than the per-anime rank Tag has.
type TagOption struct {
	Name     string `json:"name"`
	Category string `json:"category"`
	IsAdult  bool   `json:"isAdult"`
}

// Genres and tags are a fixed vocabulary AniList publishes, so the UI can offer
// them as checkboxes rather than free text that silently matches nothing.
func (c *Client) Vocabulary(ctx context.Context) ([]string, []TagOption, error) {
	var out struct {
		Genres []string    `json:"GenreCollection"`
		Tags   []TagOption `json:"MediaTagCollection"`
	}
	err := c.Query(ctx, genreQuery, nil, &out)
	return out.Genres, out.Tags, err
}

func setString(vars map[string]any, key, value string) {
	if v := strings.TrimSpace(value); v != "" {
		vars[key] = v
	}
}

func setList(vars map[string]any, key string, values []string) {
	if len(values) > 0 {
		vars[key] = values
	}
}

func hasGenre(genres []string, want string) bool {
	for _, g := range genres {
		if strings.EqualFold(strings.TrimSpace(g), want) {
			return true
		}
	}
	return false
}

func upperAll(in []string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v = strings.ToUpper(strings.TrimSpace(v)); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func clamp(v, fallback, maximum int) int {
	if v <= 0 {
		return fallback
	}
	return min(v, maximum)
}
