// Package metadata fetches per-episode data that AniList does not provide:
// episode titles and stills, filler classification, and opening/ending
// timestamps.
package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	AniZipURL  = "https://api.ani.zip/mappings?anilist_id=%d"
	AniSkipURL = "https://api.aniskip.com/v2/skip-times/%d/%d?types=op&types=ed&types=mixed-op&types=mixed-ed&types=recap&episodeLength=%d"
	FillerURL  = "https://github.com/AniraTeam/AniFiller/releases/latest/download/anifiller.min.json"
	JikanURL   = "https://api.jikan.moe/v4/anime/%d/episodes?page=%d"
	// Tenrai mirrors the same catalogue behind a Jikan-compatible schema; Jikan
	// 504s for days at a time, so relying on one host leaves recap empty.
	TenraiURL = "https://api.tenrai.org/v1/anime/%d/episodes?page=%d"
	// Cast comes from the same catalogue. MAL records it per show, not per
	// episode, so this is who is in the series rather than who is in tonight's.
	CharactersURL = "https://api.tenrai.org/v1/anime/%d/characters"

	// The site the filler dataset is built from. Its releases lag badly on
	// airing shows, so it is read only to fill episodes the dataset has no row for.
	FillerSiteIndexURL = "https://www.animefillerlist.com/shows"
	FillerSiteShowURL  = "https://www.animefillerlist.com/shows/%s"
)

type Client struct {
	http *http.Client
	urls map[string]string
}

func New() *Client {
	return &Client{
		http: &http.Client{Timeout: 60 * time.Second},
		urls: map[string]string{},
	}
}

// SetURL redirects a source, for tests.
func (c *Client) SetURL(name, url string) { c.urls[name] = url }

func (c *Client) url(name, fallback string) string {
	if v, ok := c.urls[name]; ok {
		return v
	}
	return fallback
}

type Episode struct {
	// Number is the entry's own count from one, what AniList, MAL, the airing
	// schedule and most release groups use. TvdbNumber is TheTVDB's position in
	// its season, which files a later cour past one (Bleach TYBW part 4 is
	// 41-50 there) — kept as an alias some groups release under.
	Number     int
	TvdbNumber int
	Absolute   int
	Season     int
	TitleEN    string
	TitleJA    string
	Overview   string
	Image      string
	AirDate    int64
	Runtime    int
	AniDBID    int
}

// anizipEpisode mirrors one entry of the episodes map. Both spellings of the
// renamed fields are read; TheTVDB's numbering is no longer sent at all.
type anizipEpisode struct {
	Episode        string            `json:"episode"`
	EpisodeNumber  int               `json:"episodeNumber"`
	SeasonNumber   int               `json:"seasonNumber"`
	AbsoluteNumber int               `json:"absoluteEpisodeNumber"`
	Title          map[string]string `json:"title"`
	Overview       string            `json:"overview"`
	Summary        string            `json:"summary"`
	Image          string            `json:"image"`
	AirDate        string            `json:"airDate"`
	RuntimeMinutes int               `json:"runtime"`
	Length         int               `json:"length"`
	AniDBEpisodeID int               `json:"anidbEid"`
}

func (e anizipEpisode) overview() string {
	if e.Overview != "" {
		return e.Overview
	}
	return e.Summary
}

func (e anizipEpisode) runtime() int {
	if e.RuntimeMinutes > 0 {
		return e.RuntimeMinutes
	}
	return e.Length
}

type anizipResponse struct {
	Titles   map[string]string        `json:"titles"`
	Episodes map[string]anizipEpisode `json:"episodes"`
	Mappings struct {
		MalID  int `json:"mal_id"`
		AniDB  int `json:"anidb_id"`
		TvdbID int `json:"thetvdb_id"`
	} `json:"mappings"`
}

type Mapping struct {
	MalID    int
	AniDBID  int
	TvdbID   int
	Episodes []Episode
}

// Episodes fetches per-episode data. Specials keyed "S1".."Sn" are skipped:
// they do not belong in a season's episode list.
func (c *Client) Episodes(ctx context.Context, anilistID int) (Mapping, error) {
	url := c.url("anizip", fmt.Sprintf(AniZipURL, anilistID))

	body, err := c.get(ctx, url)
	if err != nil {
		return Mapping{}, err
	}

	var res anizipResponse
	if err := json.Unmarshal(body, &res); err != nil {
		return Mapping{}, fmt.Errorf("ani.zip: %w", err)
	}

	out := Mapping{
		MalID:   res.Mappings.MalID,
		AniDBID: res.Mappings.AniDB,
		TvdbID:  res.Mappings.TvdbID,
	}

	for key, ep := range res.Episodes {
		if strings.HasPrefix(strings.ToUpper(key), "S") {
			continue
		}
		// The map key is the entry's own numbering; episodeNumber is TVDB's.
		number, _ := strconv.Atoi(key)
		if number == 0 {
			number = ep.EpisodeNumber
		}
		if number == 0 {
			continue
		}

		out.Episodes = append(out.Episodes, Episode{
			Number:     number,
			TvdbNumber: ep.EpisodeNumber,
			Absolute:   ep.AbsoluteNumber,
			Season:     ep.SeasonNumber,
			TitleEN:    ep.Title["en"],
			TitleJA:    firstOf(ep.Title, "ja", "x-jat"),
			Overview:   ep.overview(),
			Image:      ep.Image,
			AirDate:    parseDate(ep.AirDate),
			Runtime:    ep.runtime(),
			AniDBID:    ep.AniDBEpisodeID,
		})
	}
	return out, nil
}

type SkipTime struct {
	Kind   string
	Start  float64
	End    float64
	SkipID string
}

type aniskipResponse struct {
	Found   bool `json:"found"`
	Results []struct {
		Interval struct {
			Start float64 `json:"startTime"`
			End   float64 `json:"endTime"`
		} `json:"interval"`
		SkipType string `json:"skipType"`
		SkipID   string `json:"skipId"`
	} `json:"results"`
}

// Skips returns opening and ending markers. episodeLength is the player's own
// duration: passing it lets the service match the right cut of the episode.
func (c *Client) Skips(ctx context.Context, malID, episode, episodeLength int) ([]SkipTime, error) {
	if malID == 0 || episode == 0 {
		return nil, nil
	}
	url := c.url("aniskip", fmt.Sprintf(AniSkipURL, malID, episode, episodeLength))

	body, err := c.get(ctx, url)
	if err != nil {
		return nil, err
	}

	var res aniskipResponse
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, fmt.Errorf("aniskip: %w", err)
	}
	if !res.Found {
		return nil, nil
	}

	out := make([]SkipTime, 0, len(res.Results))
	for _, r := range res.Results {
		if r.Interval.End <= r.Interval.Start {
			continue
		}
		out = append(out, SkipTime{
			Kind:   r.SkipType,
			Start:  r.Interval.Start,
			End:    r.Interval.End,
			SkipID: r.SkipID,
		})
	}
	return out, nil
}

type Filler struct {
	MalID     int
	AniListID int
	Episode   int
	Kind      string
}

type fillerShow struct {
	Slug     string `json:"slug"`
	Title    string `json:"title"`
	Mappings struct {
		AniListID int `json:"anilist_id"`
		MalID     int `json:"mal_id"`
	} `json:"mappings"`
	Episodes []struct {
		Episode int    `json:"episode"`
		Type    string `json:"type"`
	} `json:"episodes"`
}

// Fillers loads the published dataset rather than scraping. Most shows are not
// in it at all, which is correct: a series with no filler has no entry.
func (c *Client) Fillers(ctx context.Context) ([]Filler, error) {
	body, err := c.getStrict(ctx, c.url("filler", FillerURL))
	if err != nil {
		return nil, err
	}

	var shows []fillerShow
	if err := json.Unmarshal(body, &shows); err != nil {
		return nil, fmt.Errorf("filler dataset: %w", err)
	}
	if len(shows) == 0 {
		return nil, fmt.Errorf("filler dataset returned no shows")
	}

	out := make([]Filler, 0, 12000)
	for _, show := range shows {
		if show.Mappings.MalID == 0 && show.Mappings.AniListID == 0 {
			continue
		}
		for _, ep := range show.Episodes {
			if ep.Episode == 0 {
				continue
			}
			out = append(out, Filler{
				MalID:     show.Mappings.MalID,
				AniListID: show.Mappings.AniListID,
				Episode:   ep.Episode,
				Kind:      normaliseKind(ep.Type),
			})
		}
	}
	return out, nil
}

// Rows carry their classification in the row's own class, and the episode
// number in its id, so neither depends on the cell layout holding still.
var (
	fillerRow    = regexp.MustCompile(`(?i)<tr class="([a-z_ ]+)"[^>]*id="eps-(\d+)"`)
	fillerShowRe = regexp.MustCompile(`(?i)<a href="/shows/([a-z0-9-]+)"[^>]*>([^<]+)</a>`)
)

// FillerSiteShows maps a normalised show title to the site's slug for it.
func (c *Client) FillerSiteShows(ctx context.Context) (map[string]string, error) {
	body, err := c.get(ctx, c.url("fillersite", FillerSiteIndexURL))
	if err != nil {
		return nil, err
	}

	out := map[string]string{}
	for _, m := range fillerShowRe.FindAllStringSubmatch(string(body), -1) {
		slug, title := m[1], html.UnescapeString(m[2])
		// Titles often carry the romanisation in brackets — "A Certain Magical
		// Index (Toaru Majutsu No Index)" — and either half may be what we hold.
		for _, part := range strings.Split(strings.NewReplacer("(", "|", ")", "").Replace(title), "|") {
			if key := NormaliseTitle(part); key != "" {
				if _, taken := out[key]; !taken {
					out[key] = slug
				}
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("filler site listed no shows")
	}
	return out, nil
}

// FillerSiteEpisodes reads one show's classifications straight from the site.
func (c *Client) FillerSiteEpisodes(ctx context.Context, slug string) ([]Filler, error) {
	if slug == "" {
		return nil, nil
	}

	body, err := c.get(ctx, c.url("fillershow", fmt.Sprintf(FillerSiteShowURL, slug)))
	if err != nil {
		return nil, err
	}

	rows := fillerRow.FindAllStringSubmatch(string(body), -1)
	if len(rows) == 0 {
		return nil, fmt.Errorf("filler site: no episodes for %s", slug)
	}

	out := make([]Filler, 0, len(rows))
	for _, m := range rows {
		number, err := strconv.Atoi(m[2])
		if err != nil || number == 0 {
			continue
		}
		// The class is "manga_canon odd" or "filler even"; only the first word
		// is the classification.
		kind := strings.ReplaceAll(strings.Fields(m[1])[0], "_", "-")
		out = append(out, Filler{Episode: number, Kind: normaliseKind(kind)})
	}
	return out, nil
}

// NormaliseTitle reduces a title to the letters and digits in it, so spelling
// and punctuation cannot keep two names for the same show apart.
func NormaliseTitle(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// The dataset separates manga canon from anime canon and marks partially
// adapted episodes, which a filler boolean would throw away.
func normaliseKind(kind string) string {
	switch k := strings.ToLower(strings.TrimSpace(kind)); k {
	case "manga-canon", "manga canon", "manga":
		return "manga-canon"
	case "anime-canon", "anime canon", "anime":
		return "anime-canon"
	case "mixed-manga", "mixed canon/filler", "mixed-canon-filler", "mixed":
		return "mixed"
	case "filler":
		return "filler"
	case "":
		return "unknown"
	default:
		return k
	}
}

type EpisodeFlags struct {
	Episode int
	Filler  bool
	Recap   bool
}

// Voice is one performance of a character. Only the two languages anyone picks
// between are kept, so a card can follow subbed vs dubbed.
type Voice struct {
	Name     string `json:"name"`
	Image    string `json:"image,omitempty"`
	Language string `json:"language"`
}

// Character is one member of a show's cast, with whoever voiced them.
type Character struct {
	ID        int     `json:"id"`
	Name      string  `json:"name"`
	Image     string  `json:"image,omitempty"`
	Role      string  `json:"role,omitempty"`
	Favorites int     `json:"favorites"`
	Voices    []Voice `json:"voices,omitempty"`
}

type charactersResponse struct {
	Data []struct {
		Character struct {
			MalID  int    `json:"mal_id"`
			Name   string `json:"name"`
			Images struct {
				JPG struct {
					ImageURL string `json:"image_url"`
				} `json:"jpg"`
			} `json:"images"`
		} `json:"character"`
		Role        string `json:"role"`
		Favorites   int    `json:"favorites"`
		VoiceActors []struct {
			Person struct {
				Name   string `json:"name"`
				Images struct {
					JPG struct {
						ImageURL string `json:"image_url"`
					} `json:"jpg"`
				} `json:"images"`
			} `json:"person"`
			Language string `json:"language"`
		} `json:"voice_actors"`
	} `json:"data"`
}

// Characters returns a show's cast, most-loved first: a long-running show lists
// thousands of one-scene parts, so the ordering is what makes the list usable.
func (c *Client) Characters(ctx context.Context, malID int) ([]Character, error) {
	if malID == 0 {
		return nil, nil
	}

	body, err := c.get(ctx, c.url("characters", fmt.Sprintf(CharactersURL, malID)))
	if err != nil {
		return nil, err
	}

	var res charactersResponse
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, fmt.Errorf("characters: %w", err)
	}

	out := make([]Character, 0, len(res.Data))
	for _, entry := range res.Data {
		ch := Character{
			ID:        entry.Character.MalID,
			Name:      entry.Character.Name,
			Image:     entry.Character.Images.JPG.ImageURL,
			Role:      entry.Role,
			Favorites: entry.Favorites,
		}

		// Japanese first: it is the performance the show was made with, and it
		// is what a card falls back to when nothing was dubbed.
		for _, want := range []string{"Japanese", "English"} {
			for _, va := range entry.VoiceActors {
				if va.Language != want {
					continue
				}
				ch.Voices = append(ch.Voices, Voice{
					Name:     va.Person.Name,
					Image:    va.Person.Images.JPG.ImageURL,
					Language: va.Language,
				})
				break
			}
		}
		out = append(out, ch)
	}

	slices.SortStableFunc(out, func(a, b Character) int { return b.Favorites - a.Favorites })
	return out, nil
}

type jikanResponse struct {
	Data []struct {
		MalID  int  `json:"mal_id"`
		Filler bool `json:"filler"`
		Recap  bool `json:"recap"`
	} `json:"data"`
	Pagination struct {
		LastPage    int  `json:"last_visible_page"`
		HasNextPage bool `json:"has_next_page"`
	} `json:"pagination"`
}

// Flags fetches per-episode filler and recap booleans; recap exists nowhere in
// the filler dataset, so this is its only source. Called lazily per show since
// Jikan paginates at 100 and allows ~3 req/s.
func (c *Client) Flags(ctx context.Context, malID int) ([]EpisodeFlags, error) {
	if malID == 0 {
		return nil, nil
	}

	// Tenrai first (maintained and answering; Jikan often 504s), Jikan as
	// fallback since both mirror the same catalogue. Whichever host answers page
	// one serves the rest, to avoid paying a dead host's timeout on every page.
	hosts := []struct{ name, pattern string }{
		{"tenrai", TenraiURL},
		{"jikan", JikanURL},
	}
	host := -1

	var out []EpisodeFlags
	for page := 1; page <= 20; page++ {
		var body []byte
		var err error

		if host >= 0 {
			body, err = c.get(ctx, c.url(hosts[host].name, fmt.Sprintf(hosts[host].pattern, malID, page)))
		} else {
			for i, h := range hosts {
				body, err = c.get(ctx, c.url(h.name, fmt.Sprintf(h.pattern, malID, page)))
				if err == nil {
					host = i
					break
				}
			}
		}
		if err != nil {
			return out, err
		}

		var res jikanResponse
		if err := json.Unmarshal(body, &res); err != nil {
			return out, fmt.Errorf("episode flags: %w", err)
		}
		if len(res.Data) == 0 {
			break
		}

		for i, ep := range res.Data {
			number := ep.MalID
			if number == 0 {
				number = (page-1)*100 + i + 1
			}
			if ep.Filler || ep.Recap {
				out = append(out, EpisodeFlags{Episode: number, Filler: ep.Filler, Recap: ep.Recap})
			}
		}

		if !res.Pagination.HasNextPage {
			break
		}
		// Paging faster than this silently returns empty pages, which
		// truncates long-running shows rather than erroring.
		select {
		case <-ctx.Done():
			return out, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return out, nil
}

func (c *Client) get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "kuro/0.1")
	req.Header.Set("Accept", "application/json")

	res, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	// A per-anime lookup that 404s just means no entry exists, which is a
	// normal outcome rather than a failure.
	if res.StatusCode == http.StatusNotFound {
		return []byte("{}"), nil
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", url, res.StatusCode)
	}
	return io.ReadAll(io.LimitReader(res.Body, 32<<20))
}

// getStrict treats a 404 as an error. For a bulk dataset a missing file is a
// broken URL, and swallowing it reports a successful load of nothing.
func (c *Client) getStrict(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "kuro/0.1")

	res, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", url, res.StatusCode)
	}
	return io.ReadAll(io.LimitReader(res.Body, 32<<20))
}

func firstOf(m map[string]string, keys ...string) string {
	for _, k := range keys {
		if v := m[k]; v != "" {
			return v
		}
	}
	return ""
}

func parseDate(s string) int64 {
	if s == "" {
		return 0
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02", "2006-01-02T15:04:05Z"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Unix()
		}
	}
	return 0
}
