package anilist

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

const OAuthBase = "https://anilist.co/api/v2/oauth"

// WithOAuthBase redirects the authorize and token endpoints, for tests.
func WithOAuthBase(base string) Option {
	return func(c *Client) { c.oauthBase = base }
}

// AniList has no PKCE or usable implicit flow (implicit token lands in a URL
// fragment the server never sees), so Authorization Code is the only option.
func (c *Client) AuthorizeURL(clientID, redirectURI, state string) string {
	q := url.Values{
		"client_id":     {clientID},
		"redirect_uri":  {redirectURI},
		"response_type": {"code"},
		"state":         {state},
	}
	return c.oauthBase + "/authorize?" + q.Encode()
}

type Token struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

// Tokens last about a year and there are no refresh tokens, so expiry is a
// once-a-year manual re-authentication rather than something to automate.
func (t Token) ExpiresAt() time.Time {
	return time.Now().Add(time.Duration(t.ExpiresIn) * time.Second)
}

func (c *Client) Exchange(ctx context.Context, clientID, clientSecret, redirectURI, code string) (Token, error) {
	body, err := json.Marshal(map[string]string{
		"grant_type":    "authorization_code",
		"client_id":     clientID,
		"client_secret": clientSecret,
		"redirect_uri":  redirectURI,
		"code":          code,
	})
	if err != nil {
		return Token{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.oauthBase+"/token", bytes.NewReader(body))
	if err != nil {
		return Token{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Referer", Referer)

	res, err := c.http.Do(req)
	if err != nil {
		return Token{}, err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	if err != nil {
		return Token{}, err
	}

	if res.StatusCode != http.StatusOK {
		return Token{}, fmt.Errorf("anilist token exchange (HTTP %d): %s", res.StatusCode, exchangeError(raw))
	}

	var token Token
	if err := json.Unmarshal(raw, &token); err != nil {
		return Token{}, fmt.Errorf("anilist token exchange: %w: %s", err, snippet(raw))
	}
	if token.AccessToken == "" {
		return Token{}, fmt.Errorf("anilist token exchange: empty access_token")
	}
	return token, nil
}

// exchangeError is what the OAuth server said: its JSON fields, or the start of a CDN or WAF page.
func exchangeError(raw []byte) string {
	var e struct {
		Error       string `json:"error"`
		Description string `json:"error_description"`
		Hint        string `json:"hint"`
		Message     string `json:"message"`
	}
	if json.Unmarshal(raw, &e) == nil {
		if said := strings.Join(slices.DeleteFunc([]string{e.Error, e.Description, e.Hint, e.Message},
			func(s string) bool { return s == "" }), ": "); said != "" {
			return said
		}
	}
	return snippet(raw)
}

func snippet(raw []byte) string {
	s := strings.Join(strings.Fields(string(raw)), " ")
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	if s == "" {
		return "(empty body)"
	}
	return s
}

const viewerQuery = `query { Viewer { id name mediaListOptions { scoreFormat } } }`

type Viewer struct {
	ID               int    `json:"id"`
	Name             string `json:"name"`
	MediaListOptions struct {
		ScoreFormat string `json:"scoreFormat"`
	} `json:"mediaListOptions"`
}

// List queries never infer the authenticated user, so this is fetched once at login and cached.
func (c *Client) Viewer(ctx context.Context) (Viewer, error) {
	var out struct {
		Viewer Viewer `json:"Viewer"`
	}
	err := c.Query(ctx, viewerQuery, nil, &out)
	return out.Viewer, err
}

// ViewerWith asks who a token belongs to without installing it: a login that fails halfway changes nothing.
func (c *Client) ViewerWith(ctx context.Context, token string) (Viewer, error) {
	return c.Viewer(context.WithValue(ctx, tokenKey{}, token))
}
