// Package mal talks to the MyAnimeList API v2. It exists alongside anilist as
// a second tracker: MAL uses its own anime ids, its own status vocabulary, and
// access tokens that expire hourly rather than yearly.
package mal

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const (
	API       = "https://api.myanimelist.net/v2"
	OAuthBase = "https://myanimelist.net/v1/oauth2"

	// MAL publishes no rate limit. This is deliberately gentle: a full list
	// pull is a handful of requests and progress pushes are one per episode.
	requestsPerSecond = 1
	burst             = 3
	maxRetries        = 3
)

// Token is what has to be persisted for the connection to survive a restart.
type Token struct {
	Access  string
	Refresh string
	Expires time.Time
}

type Client struct {
	http    *http.Client
	limiter *rate.Limiter
	log     *slog.Logger
	api     string
	oauth   string

	// onToken persists a refreshed token. Refresh happens mid-request, so the
	// caller cannot be the one to notice.
	onToken func(Token)
	backoff func(int) time.Duration

	// Credentials are guarded because they are entered in the app rather than
	// read from a file, so they can change while requests are in flight.
	mu       sync.Mutex
	token    Token
	clientID string
	secret   string
}

type Option func(*Client)

func WithAPI(u string) Option       { return func(c *Client) { c.api = u } }
func WithOAuthBase(u string) Option { return func(c *Client) { c.oauth = u } }
func WithoutRateLimit() Option      { return func(c *Client) { c.limiter = rate.NewLimiter(rate.Inf, 1) } }

// withBackoff shortens the retry delay so tests can exercise retries without
// waiting seven seconds for them.
func withBackoff(f func(int) time.Duration) Option { return func(c *Client) { c.backoff = f } }
func OnToken(f func(Token)) Option                 { return func(c *Client) { c.onToken = f } }
func WithCredentials(id, secret string) Option {
	return func(c *Client) { c.clientID, c.secret = id, secret }
}

func New(log *slog.Logger, opts ...Option) *Client {
	c := &Client{
		http:    &http.Client{Timeout: 30 * time.Second},
		limiter: rate.NewLimiter(rate.Limit(requestsPerSecond), burst),
		log:     log,
		api:     API,
		oauth:   OAuthBase,
		backoff: backoff,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func (c *Client) SetToken(t Token) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.token = t
}

func (c *Client) Token() Token {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.token
}

// SetCredentials swaps the registered application, for when they are saved in
// the app long after startup.
func (c *Client) SetCredentials(id, secret string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.clientID, c.secret = id, secret
}

func (c *Client) credentials() (id, secret string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.clientID, c.secret
}

func (c *Client) ClientID() string {
	id, _ := c.credentials()
	return id
}

func (c *Client) Authenticated() bool { return c.Token().Access != "" }
func (c *Client) Configured() bool    { return c.ClientID() != "" }

// Error carries MAL's error body. The API answers with a JSON object rather
// than an error array, so unlike AniList the HTTP status is authoritative.
type Error struct {
	Status  int    `json:"-"`
	Kind    string `json:"error"`
	Message string `json:"message"`
}

func (e *Error) Error() string {
	parts := []string{fmt.Sprintf("mal: HTTP %d", e.Status)}
	if e.Kind != "" {
		parts = append(parts, e.Kind)
	}
	if e.Message != "" {
		parts = append(parts, e.Message)
	}
	return strings.Join(parts, ": ")
}

// Unauthorized reports whether the user has to reconnect. A refresh token that
// has expired comes back as 400 invalid_grant from the token endpoint.
func (e *Error) Unauthorized() bool {
	return e.Status == http.StatusUnauthorized ||
		(e.Status == http.StatusBadRequest && e.Kind == "invalid_grant")
}

func Unauthorized(err error) bool {
	var e *Error
	if ok := asError(err, &e); ok {
		return e.Unauthorized()
	}
	return false
}

// get calls an authenticated endpoint. absolute is used for paging, where MAL
// hands back a full URL rather than an offset.
func (c *Client) get(ctx context.Context, absolute string, out any) error {
	return c.do(ctx, http.MethodGet, absolute, nil, out)
}

func (c *Client) do(ctx context.Context, method, endpoint string, form url.Values, out any) error {
	if err := c.ensureToken(ctx); err != nil {
		return err
	}

	for attempt := 0; ; attempt++ {
		if err := c.limiter.Wait(ctx); err != nil {
			return err
		}

		var body io.Reader
		if form != nil {
			body = strings.NewReader(form.Encode())
		}
		req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
		if err != nil {
			return err
		}
		req.Header.Set("Accept", "application/json")
		if form != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		if token := c.Token().Access; token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		} else if id := c.ClientID(); id != "" {
			req.Header.Set("X-MAL-CLIENT-ID", id)
		}

		res, raw, err := c.send(req)
		if err != nil {
			if attempt < maxRetries {
				c.sleep(ctx, c.backoff(attempt))
				continue
			}
			return err
		}

		if res.StatusCode == http.StatusTooManyRequests || res.StatusCode >= 500 {
			if attempt < maxRetries {
				c.log.Warn("mal retrying", "status", res.StatusCode, "attempt", attempt+1)
				c.sleep(ctx, c.backoff(attempt))
				continue
			}
		}
		if res.StatusCode < 200 || res.StatusCode >= 300 {
			return decodeError(res.StatusCode, raw)
		}
		if out == nil {
			return nil
		}
		return json.Unmarshal(raw, out)
	}
}

func (c *Client) send(req *http.Request) (*http.Response, []byte, error) {
	res, err := c.http.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer res.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(res.Body, 32<<20))
	return res, raw, err
}

func decodeError(status int, raw []byte) error {
	e := &Error{Status: status}
	// A non-JSON body means an edge server answered, not the API.
	if err := json.Unmarshal(raw, e); err != nil {
		e.Message = strings.TrimSpace(string(raw))
		if len(e.Message) > 200 {
			e.Message = e.Message[:200]
		}
	}
	e.Status = status
	return e
}

func (c *Client) sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

func backoff(attempt int) time.Duration {
	return time.Duration(1<<attempt) * time.Second
}
