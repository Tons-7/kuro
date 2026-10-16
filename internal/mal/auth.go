package mal

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// refreshWindow renews early, so a slow request can't start valid and finish expired.
const refreshWindow = 5 * time.Minute

// Verifier is the PKCE secret; MAL takes only the plain method, so it leaves only in the token exchange.
func Verifier() string {
	b := make([]byte, 64)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// AuthURL is where the user authorises the app. state is checked on return.
func (c *Client) AuthURL(verifier, state, redirect string) string {
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {c.ClientID()},
		"code_challenge":        {verifier},
		"code_challenge_method": {"plain"},
		"state":                 {state},
	}
	if redirect != "" {
		q.Set("redirect_uri", redirect)
	}
	return c.oauth + "/authorize?" + q.Encode()
}

type tokenResponse struct {
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

func (c *Client) Exchange(ctx context.Context, code, verifier, redirect string) (Token, error) {
	id, secret := c.credentials()
	form := url.Values{
		"client_id":     {id},
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"code_verifier": {verifier},
	}
	if secret != "" {
		form.Set("client_secret", secret)
	}
	if redirect != "" {
		form.Set("redirect_uri", redirect)
	}
	return c.token1(ctx, form, "")
}

// Refresh trades the refresh token for a new pair. MAL revokes the old access
// token immediately, so the result must be stored before the next request.
func (c *Client) Refresh(ctx context.Context) (Token, error) {
	current := c.Token()
	if current.Refresh == "" {
		return Token{}, errors.New("mal: no refresh token")
	}

	id, secret := c.credentials()
	form := url.Values{
		"client_id":     {id},
		"grant_type":    {"refresh_token"},
		"refresh_token": {current.Refresh},
	}
	if secret != "" {
		form.Set("client_secret", secret)
	}
	return c.token1(ctx, form, current.Refresh)
}

// errSuperseded: the account was disconnected or replaced while a refresh was in flight.
var errSuperseded = errors.New("mal: token changed during refresh")

// token1 posts to the OAuth endpoint, outside do(): no API host, no bearer. spent is the refresh token traded.
func (c *Client) token1(ctx context.Context, form url.Values, spent string) (Token, error) {
	if !c.Configured() {
		return Token{}, errors.New("mal: client_id not configured")
	}
	if err := c.limiter.Wait(ctx); err != nil {
		return Token{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.oauth+"/token", strings.NewReader(form.Encode()))
	if err != nil {
		return Token{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	res, raw, err := c.send(req)
	if err != nil {
		return Token{}, err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return Token{}, decodeError(res.StatusCode, raw)
	}

	var parsed tokenResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return Token{}, err
	}
	if parsed.AccessToken == "" {
		return Token{}, errors.New("mal: token response had no access token")
	}

	token := Token{
		Access:  parsed.AccessToken,
		Refresh: parsed.RefreshToken,
		Expires: time.Now().Add(time.Duration(parsed.ExpiresIn) * time.Second),
	}
	// A refresh response without a new refresh token leaves the old one valid.
	if token.Refresh == "" {
		token.Refresh = spent
	}

	// A logout or a new login since the request went out wins: storing this would reconnect the old account.
	c.mu.Lock()
	if spent != "" && c.token.Refresh != spent {
		c.mu.Unlock()
		return Token{}, errSuperseded
	}
	c.token = token
	c.mu.Unlock()
	if c.onToken != nil {
		c.onToken(token)
	}
	return token, nil
}

// Forget drops the token and runs clear with no refresh in flight, so none can write the token back.
func (c *Client) Forget(clear func() error) error {
	c.refreshing.Lock()
	defer c.refreshing.Unlock()
	c.SetToken(Token{})
	return clear()
}

// ensureToken renews before the hour is up, or every later call would 401.
func (c *Client) ensureToken(ctx context.Context) error {
	due := func() bool {
		t := c.Token()
		return t.Access != "" && !t.Expires.IsZero() &&
			time.Until(t.Expires) <= refreshWindow && t.Refresh != ""
	}
	if !due() {
		return nil
	}
	c.refreshing.Lock()
	defer c.refreshing.Unlock()
	// Another caller may have refreshed while this one waited.
	if !due() {
		return nil
	}

	if _, err := c.Refresh(ctx); errors.Is(err, errSuperseded) {
		return nil // the request goes out with whatever replaced it
	} else if err != nil {
		c.log.Warn("mal token refresh failed", "err", err)
		return err
	}
	c.log.Info("mal token refreshed")
	return nil
}

func asError(err error, target **Error) bool {
	return errors.As(err, target)
}
