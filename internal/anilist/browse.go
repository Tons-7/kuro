package anilist

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"golang.org/x/time/rate"
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

// browseSorts are the orderings the API accepts here. A typed query with no
// sort chosen falls back to match quality.
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

// Shared by the page query and the count probes, so both see the same filters.
const browseVarDecls = `$search: String, $genres: [String], $excludeGenres: [String], $tags: [String],
  $year: Int, $season: MediaSeason, $formats: [MediaFormat], $statuses: [MediaStatus],
  $minScore: Int, $minEpisodes: Int, $maxEpisodes: Int, $country: CountryCode,
  $source: MediaSource, $isAdult: Boolean, $sort: [MediaSort]`

const browseMediaArgs = `type: ANIME
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
      sort: $sort`

const browseQuery = `query Browse(` + browseVarDecls + `, $page: Int!, $perPage: Int!) {
  Page(page: $page, perPage: $perPage) {
    pageInfo { total currentPage lastPage hasNextPage }
    media(` + browseMediaArgs + `) {` + mediaFields + `}
  }
}`

func (c *Client) BrowseMedia(ctx context.Context, b Browse) (DiscoverPage, error) {
	vars := browseVars(b)
	vars["page"] = max(b.Page, 1)
	vars["perPage"] = clamp(b.PerPage, 30, 50)

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

// BrowseCap is as deep as AniList pages a query; past it pages come back empty.
const BrowseCap = 5000

// probesPerRound keeps one request well inside AniList's complexity limit.
const probesPerRound = 24

// ErrBusy: counting is a nicety, so it waits while the shared budget is needed elsewhere.
var ErrBusy = errors.New("anilist: request budget busy")

// countReserve is how much of the burst a count leaves for searches and playback.
const countReserve = 3

// BrowseCount is how many results b matches, up to BrowseCap. Paged answers report a fixed
// total of 5000, so it probes one-result pages; known is a position known to exist.
func (c *Client) BrowseCount(ctx context.Context, b Browse, known int) (int, error) {
	// Checked once: a count started is finished, or the requests already spent are wasted.
	if c.limiter.Limit() != rate.Inf && c.limiter.Tokens() < countReserve {
		return 0, ErrBusy
	}
	lo, hi := max(known, 1), BrowseCap+1 // lo exists, hi does not
	for range 6 {
		if hi-lo <= 1 {
			break
		}
		probes := spread(lo+1, hi-1, probesPerRound)
		found, err := c.probe(ctx, b, probes)
		if err != nil {
			return 0, err
		}
		for _, k := range probes {
			p := found[k]
			if !p.exists {
				hi = k
				break
			}
			if !p.more {
				return k, nil
			}
			lo = k
		}
	}
	return lo, nil
}

type probeResult struct{ exists, more bool }

func (c *Client) probe(ctx context.Context, b Browse, positions []int) (map[int]probeResult, error) {
	var q strings.Builder
	q.WriteString("query Count(" + browseVarDecls + ") {\n")
	for _, k := range positions {
		fmt.Fprintf(&q, "  p%d: Page(page: %d, perPage: 1) { pageInfo { hasNextPage } media(%s) { id } }\n", k, k, browseMediaArgs)
	}
	q.WriteString("}")

	var out map[string]struct {
		PageInfo struct {
			HasNextPage bool `json:"hasNextPage"`
		} `json:"pageInfo"`
		Media []struct{ ID int } `json:"media"`
	}
	if err := c.Query(ctx, q.String(), browseVars(b), &out); err != nil {
		return nil, err
	}
	found := make(map[int]probeResult, len(positions))
	for _, k := range positions {
		p := out[fmt.Sprintf("p%d", k)]
		found[k] = probeResult{exists: len(p.Media) > 0, more: p.PageInfo.HasNextPage}
	}
	return found, nil
}

// spread picks up to n positions in [from, to], geometric over a wide range so small sets bracket early.
func spread(from, to, n int) []int {
	if to < from {
		return nil
	}
	if to-from+1 <= n {
		out := make([]int, 0, to-from+1)
		for k := from; k <= to; k++ {
			out = append(out, k)
		}
		return out
	}
	out := make([]int, 0, n)
	geometric := to > from*4
	for i := 1; i <= n; i++ {
		var k int
		if geometric {
			k = int(float64(from) * math.Pow(float64(to)/float64(from), float64(i)/float64(n)))
		} else {
			k = from + (to-from)*i/n
		}
		if k >= from && k <= to && (len(out) == 0 || k > out[len(out)-1]) {
			out = append(out, k)
		}
	}
	return out
}

// browseVars are the filter variables; page and perPage are the caller's.
func browseVars(b Browse) map[string]any {
	vars := map[string]any{"isAdult": nil}

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

	// Nothing chosen: relevance for a typed query, popularity otherwise. A sort
	// the user picked is a request to reorder those matches, so it wins.
	sort := browseSorts[strings.ToLower(b.Sort)]
	if sort == "" {
		sort = "POPULARITY_DESC"
		if b.Search != "" {
			sort = "SEARCH_MATCH"
		}
	}
	vars["sort"] = []string{sort}
	return vars
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
