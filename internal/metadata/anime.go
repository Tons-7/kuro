package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

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

// Anime fetches one record for a show AniList never catalogued. Tenrai leads
// because Jikan often 504s on exactly these obscure titles; both mirror the
// same catalogue, so either will do.
func (c *Client) Anime(ctx context.Context, malID int) (AnimeDetail, error) {
	if malID <= 0 {
		return AnimeDetail{}, fmt.Errorf("metadata: invalid mal id %d", malID)
	}

	hosts := []struct{ name, pattern string }{
		{"tenrai-anime", TenraiAnimeURL},
		{"jikan-anime", JikanAnimeURL},
	}

	// Retried across both: a second attempt at the same host often lands because
	// the first one warmed its cache.
	var body []byte
	var err error
	for attempt := range 3 {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return AnimeDetail{}, ctx.Err()
			case <-time.After(time.Duration(attempt) * time.Second):
			}
		}
		for _, host := range hosts {
			if body, err = c.get(ctx, c.url(host.name, fmt.Sprintf(host.pattern, malID))); err == nil {
				break
			}
		}
		if err == nil {
			break
		}
	}
	if err != nil {
		return AnimeDetail{}, err
	}

	var res jikanAnime
	if err := json.Unmarshal(body, &res); err != nil {
		return AnimeDetail{}, fmt.Errorf("anime record: %w", err)
	}
	d := res.Data
	if d.MalID == 0 {
		return AnimeDetail{}, fmt.Errorf("anime %d not found", malID)
	}

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
