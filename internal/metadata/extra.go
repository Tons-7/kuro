package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
)

const (
	TenraiFullURL  = "https://api.tenrai.org/v1/anime/%d/full"
	TenraiStaffURL = "https://api.tenrai.org/v1/anime/%d/staff"
	JikanFullURL   = "https://api.jikan.moe/v4/anime/%d/full"
	JikanStaffURL  = "https://api.jikan.moe/v4/anime/%d/staff"
)

// Theme is one opening or ending, shipped upstream as a single line
// (`1: "Yuusha (勇者)" by YOASOBI (eps 1-16)`), taken apart here so it can be laid out.
type Theme struct {
	Title    string `json:"title"`
	Artist   string `json:"artist,omitempty"`
	Episodes string `json:"episodes,omitempty"`
}

// Credit is one person and what they did.
type Credit struct {
	Name  string `json:"name"`
	Role  string `json:"role"`
	Image string `json:"image,omitempty"`
}

// Extra is what a show has beyond its episodes and cast.
type Extra struct {
	Openings []Theme  `json:"openings,omitempty"`
	Endings  []Theme  `json:"endings,omitempty"`
	Staff    []Credit `json:"staff,omitempty"`
	Trailer  string   `json:"trailer,omitempty"`
	// StaffMissing: the staff request failed, so the answer is partial and worth asking again soon.
	StaffMissing bool `json:"-"`
}

type fullResponse struct {
	Data struct {
		MalID int `json:"mal_id"`
		Theme struct {
			Openings []string `json:"openings"`
			Endings  []string `json:"endings"`
		} `json:"theme"`
		Trailer struct {
			URL string `json:"url"`
		} `json:"trailer"`
	} `json:"data"`
}

type staffResponse struct {
	Data []struct {
		Person struct {
			Name   string `json:"name"`
			Images struct {
				JPG struct {
					ImageURL string `json:"image_url"`
				} `json:"jpg"`
			} `json:"images"`
		} `json:"person"`
		Positions []string `json:"positions"`
	} `json:"data"`
}

// The roles people read a staff list for, in reading order.
var keyRoles = []string{
	"Original Creator",
	"Director",
	"Series Composition",
	"Character Design",
	"Music",
	"Sound Director",
	"Art Director",
}

// A position carries its scope in brackets — "Storyboard (eps 1, 11, 21)" — and
// the scope is what makes it a minor credit rather than a defining one.
var roleScope = regexp.MustCompile(`\s*\([^)]*\)\s*$`)

var (
	themeIndex = regexp.MustCompile(`^S?\d+:\s*`)
	themeEps   = regexp.MustCompile(`\s*\(eps?\s+([^)]*)\)\s*$`)
)

// Titles contain quotes of their own (`"Step - Step" by Ziggy`), so the split is
// taken at the last `" by`, not the first; anything else is kept whole.
func parseTheme(line string) Theme {
	rest := strings.TrimSpace(themeIndex.ReplaceAllString(strings.TrimSpace(line), ""))

	var out Theme
	if m := themeEps.FindStringSubmatch(rest); m != nil {
		out.Episodes = strings.TrimSpace(m[1])
		rest = themeEps.ReplaceAllString(rest, "")
	}

	if cut := strings.LastIndex(rest, `" by `); cut != -1 {
		out.Title = strings.Trim(rest[:cut], `"`)
		out.Artist = strings.TrimSpace(rest[cut+len(`" by `):])
		return out
	}
	if cut := strings.LastIndex(rest, " by "); cut != -1 {
		out.Title = strings.Trim(rest[:cut], `" `)
		out.Artist = strings.TrimSpace(rest[cut+len(" by "):])
		return out
	}

	out.Title = strings.Trim(rest, `"`)
	return out
}

// Extra reads themes, key staff and trailer: two requests, so asked for apart from the details.
func (c *Client) Extra(ctx context.Context, malID int) (Extra, error) {
	if malID == 0 {
		return Extra{}, nil
	}

	var full fullResponse
	_, err := c.fromHosts(ctx, []host{{"tenrai-full", TenraiFullURL}, {"jikan-full", JikanFullURL}}, malID,
		func(body []byte) bool {
			full = fullResponse{}
			return json.Unmarshal(body, &full) == nil && full.Data.MalID != 0
		})
	if errors.Is(err, errNoRecord) {
		return Extra{}, nil
	}
	if err != nil {
		return Extra{}, err
	}

	out := Extra{Trailer: full.Data.Trailer.URL}
	for _, line := range full.Data.Theme.Openings {
		out.Openings = append(out.Openings, parseTheme(line))
	}
	for _, line := range full.Data.Theme.Endings {
		out.Endings = append(out.Endings, parseTheme(line))
	}

	// Staff is the second request and the less important half: a show with no credits is still worth its themes.
	var staff staffResponse
	_, err = c.fromHosts(ctx, []host{{"tenrai-staff", TenraiStaffURL}, {"jikan-staff", JikanStaffURL}}, malID,
		func(body []byte) bool {
			staff = staffResponse{}
			return json.Unmarshal(body, &staff) == nil && len(staff.Data) > 0
		})
	if err != nil && !errors.Is(err, errNoRecord) {
		out.StaffMissing = true
		return out, nil
	}

	// Grouped by role so the list reads in a fixed order; each role keeps only
	// its first couple, since a long-running show credits many for one job.
	const perRole = 2
	seen := map[string]bool{}
	for _, role := range keyRoles {
		var kept int
		for _, entry := range staff.Data {
			if kept >= perRole {
				break
			}
			for _, position := range entry.Positions {
				if roleScope.ReplaceAllString(position, "") != role {
					continue
				}
				if key := role + "|" + entry.Person.Name; !seen[key] {
					seen[key] = true
					kept++
					out.Staff = append(out.Staff, Credit{
						Name:  entry.Person.Name,
						Role:  role,
						Image: entry.Person.Images.JPG.ImageURL,
					})
				}
				break
			}
		}
	}
	return out, nil
}
