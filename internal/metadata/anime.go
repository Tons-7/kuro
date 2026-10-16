package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type host struct{ name, pattern string }

// hostWait bounds one host's answer, so a stalled first host still leaves the caller's budget for the next.
const hostWait = 4 * time.Second

// errNoRecord: every host answered, and none had the record.
var errNoRecord = errors.New("metadata: no host has the record")

// fromHosts returns the first body found accepts; a host without the record passes to the next.
func (c *Client) fromHosts(ctx context.Context, hosts []host, malID int, found func([]byte) bool) ([]byte, error) {
	var err error
	empty := 0
	for _, h := range hosts {
		hctx, cancel := context.WithTimeout(ctx, hostWait)
		body, gerr := c.get(hctx, c.url(h.name, fmt.Sprintf(h.pattern, malID)))
		cancel()
		switch {
		case gerr != nil:
			err = gerr
		case found(body):
			return body, nil
		default:
			empty++
		}
	}
	if empty == len(hosts) {
		return nil, errNoRecord
	}
	return nil, err
}

const (
	JikanAnimeURL  = "https://api.jikan.moe/v4/anime/%d"
	TenraiAnimeURL = "https://api.tenrai.org/v1/anime/%d"
)

// AnimeDetail is the subset of Jikan's record kuro shows for anime that exist
// on MyAnimeList but not AniList, where there is no AniList entry to read.
type AnimeDetail struct {
	MalID       int      `json:"malId"`
	Romaji      string   `json:"romaji"`
	English     string   `json:"english"`
	Native      string   `json:"native"`
	Synonyms    []string `json:"synonyms"`
	Format      string   `json:"format"`
	Status      string   `json:"status"`
	Episodes    int      `json:"episodes"`
	Duration    string   `json:"duration"`
	Season      string   `json:"season"`
	Year        int      `json:"year"`
	Score       float64  `json:"score"`
	Members     int      `json:"members"`
	Favourites  int      `json:"favourites"`
	Description string   `json:"description"`
	Cover       string   `json:"cover"`
	Trailer     string   `json:"trailer"`
	Genres      []string `json:"genres"`
	Studios     []string `json:"studios"`
	Source      string   `json:"source"`
	URL         string   `json:"url"`
}

type jikanAnime struct {
	Data struct {
		MalID  int `json:"mal_id"`
		URL    string
		Images struct {
			JPG struct {
				LargeImageURL string `json:"large_image_url"`
			} `json:"jpg"`
			WebP struct {
				LargeImageURL string `json:"large_image_url"`
			} `json:"webp"`
		} `json:"images"`
		Trailer struct {
			URL string `json:"url"`
		} `json:"trailer"`
		Title         string   `json:"title"`
		TitleEnglish  string   `json:"title_english"`
		TitleJapanese string   `json:"title_japanese"`
		TitleSynonyms []string `json:"title_synonyms"`
		Type          string   `json:"type"`
		Source        string   `json:"source"`
		Episodes      int      `json:"episodes"`
		Status        string   `json:"status"`
		Duration      string   `json:"duration"`
		Score         float64  `json:"score"`
		Members       int      `json:"members"`
		Favorites     int      `json:"favorites"`
		Synopsis      string   `json:"synopsis"`
		Season        string   `json:"season"`
		Year          int      `json:"year"`
		Genres        []struct {
			Name string `json:"name"`
		} `json:"genres"`
		Studios []struct {
			Name string `json:"name"`
		} `json:"studios"`
	} `json:"data"`
}

// Anime fetches a show AniList never catalogued; Tenrai leads, as Jikan often 504s on these.
func (c *Client) Anime(ctx context.Context, malID int) (AnimeDetail, error) {
	if malID <= 0 {
		return AnimeDetail{}, fmt.Errorf("metadata: invalid mal id %d", malID)
	}

	hosts := []host{{"tenrai-anime", TenraiAnimeURL}, {"jikan-anime", JikanAnimeURL}}

	// Retried across both: a second attempt at the same host often lands because the first one warmed its cache.
	var res jikanAnime
	var err error
	for attempt := range 3 {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return AnimeDetail{}, ctx.Err()
			case <-time.After(time.Duration(attempt) * time.Second):
			}
		}
		_, err = c.fromHosts(ctx, hosts, malID, func(body []byte) bool {
			res = jikanAnime{}
			return json.Unmarshal(body, &res) == nil && res.Data.MalID != 0
		})
		if err == nil || errors.Is(err, errNoRecord) {
			break
		}
	}
	if errors.Is(err, errNoRecord) {
		return AnimeDetail{}, fmt.Errorf("anime %d not found", malID)
	}
	if err != nil {
		return AnimeDetail{}, err
	}
	d := res.Data

	out := AnimeDetail{
		MalID:       d.MalID,
		Romaji:      d.Title,
		English:     d.TitleEnglish,
		Native:      d.TitleJapanese,
		Synonyms:    d.TitleSynonyms,
		Format:      d.Type,
		Status:      d.Status,
		Episodes:    d.Episodes,
		Duration:    d.Duration,
		Season:      d.Season,
		Year:        d.Year,
		Score:       d.Score,
		Members:     d.Members,
		Favourites:  d.Favorites,
		Description: d.Synopsis,
		Cover:       d.Images.WebP.LargeImageURL,
		Trailer:     d.Trailer.URL,
		Source:      d.Source,
		URL:         d.URL,
	}
	if out.Cover == "" {
		out.Cover = d.Images.JPG.LargeImageURL
	}
	for _, g := range d.Genres {
		out.Genres = append(out.Genres, g.Name)
	}
	for _, s := range d.Studios {
		out.Studios = append(out.Studios, s.Name)
	}
	return out, nil
}
