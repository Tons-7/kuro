package anilist

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const (
	Endpoint = "https://graphql.anilist.co"

	// AniList has been capped at 30/min since November 2022. The documented 90
	// has never been restored, so this is the real ceiling, not a safety margin.
	requestsPerMinute = 30
	burst             = 5
	maxRetries        = 3
)

type Client struct {
	http      *http.Client
	limiter   *rate.Limiter
	log       *slog.Logger
	token     string
	endpoint  string
	oauthBase string

	cache  *responseCache
	totals totals

	mu          sync.Mutex
	pausedUntil time.Time
}

type Option func(*Client)

// WithEndpoint redirects the client, for tests and for pointing at a mirror.
func WithEndpoint(url string) Option {
	return func(c *Client) { c.endpoint = url }
}

// WithoutRateLimit removes the pacing. Only safe against a local server.
func WithoutRateLimit() Option {
	return func(c *Client) { c.limiter = rate.NewLimiter(rate.Inf, 1) }
}

func New(log *slog.Logger, opts ...Option) *Client {
	c := &Client{
		http:      &http.Client{Timeout: 30 * time.Second},
		limiter:   rate.NewLimiter(rate.Every(time.Minute/requestsPerMinute), burst),
		log:       log,
		endpoint:  Endpoint,
		oauthBase: OAuthBase,
		cache:     newResponseCache(),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func (c *Client) SetToken(token string) { c.token = token }
func (c *Client) Authenticated() bool   { return c.token != "" }

// GraphQLError carries AniList's error array. Validation is populated when a
// mutation is rejected field by field.
type GraphQLError struct {
	Message    string              `json:"message"`
	Status     int                 `json:"status"`
	Validation map[string][]string `json:"validation"`
}

type Errors []GraphQLError

func (e Errors) Error() string {
	msgs := make([]string, len(e))
	for i, err := range e {
		msgs[i] = err.Message
	}
	return "anilist: " + strings.Join(msgs, "; ")
}

// Unauthorized reports whether re-authentication is required: 400 "Invalid
// token" is malformed, 401 is absent or expired. A 403 means the API is down.
func (e Errors) Unauthorized() bool {
	for _, err := range e {
		if err.Status == 401 || (err.Status == 400 && strings.Contains(err.Message, "Invalid token")) {
			return true
		}
	}
	return false
}

type response struct {
	Data   json.RawMessage `json:"data"`
	Errors Errors          `json:"errors"`
}

// queryAnonymous runs a document with the account's token withheld, which is
// the only way to see titles AniList hides from it.
func (c *Client) queryAnonymous(ctx context.Context, query string, vars map[string]any, out any) error {
	if c.token == "" {
		return errors.New("anilist: already anonymous")
	}
	// Shared cache: the key carries the token, so the two cannot cross.
	anon := &Client{
		http: c.http, limiter: c.limiter, log: c.log,
		endpoint: c.endpoint, oauthBase: c.oauthBase, cache: c.cache,
	}
	return anon.Query(ctx, query, vars, out)
}

// Query executes a document and unmarshals the data field into out. Reads are
// served from a short-lived response cache; see cache.go for why.
func (c *Client) Query(ctx context.Context, query string, vars map[string]any, out any) error {
	body, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		return err
	}

	if c.cache == nil || isMutation(query) {
		return c.execute(ctx, query, body, out)
	}

	key := cacheKey(query, body, c.token)
	if data, ok := c.cache.get(key); ok {
		return decodeInto(data, out)
	}

	// Another caller is already asking for exactly this. Wait for it and take
	// its answer rather than spending a second request from the same budget.
	wg, mine := c.cache.enter(key)
	if !mine {
		wg.Wait()
		if data, ok := c.cache.get(key); ok {
			return decodeInto(data, out)
		}
		// It failed, and the reason is worth reproducing rather than reporting
		// second-hand.
		return c.execute(ctx, query, body, out)
	}
	defer c.cache.done(key, wg)

	var data json.RawMessage
	if err := c.execute(ctx, query, body, &data); err != nil {
		return err
	}
	c.cache.put(key, data)
	return decodeInto(data, out)
}

// QueryFresh skips the response cache, for state this client also changes:
// a cached answer from before a mutation reads as the user undoing it.
func (c *Client) QueryFresh(ctx context.Context, query string, vars map[string]any, out any) error {
	body, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		return err
	}
	return c.execute(ctx, query, body, out)
}

func decodeInto(data []byte, out any) error {
	if out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}

func (c *Client) execute(ctx context.Context, query string, body []byte, out any) error {
	for attempt := 0; ; attempt++ {
		if err := c.limiter.Wait(ctx); err != nil {
			return err
		}
		if err := c.awaitPause(ctx); err != nil {
			return err
		}

		res, raw, err := c.post(ctx, body)
		if err != nil {
			if attempt < maxRetries {
				c.sleep(ctx, backoff(attempt))
				continue
			}
			return err
		}

		switch {
		case res.StatusCode == http.StatusTooManyRequests:
			wait := retryAfter(res)
			c.pauseFor(wait)
			if attempt >= maxRetries {
				return fmt.Errorf("anilist: rate limited after %d attempts", attempt)
			}
			c.log.Warn("anilist rate limited", "wait", wait)
			continue

		// 403 means the API is temporarily disabled rather than a permissions
		// problem, so it is worth retrying.
		case res.StatusCode == http.StatusForbidden || res.StatusCode >= 500:
			if attempt < maxRetries {
				c.sleep(ctx, backoff(attempt))
				continue
			}
		}

		// This response already succeeded, so hold back the next caller rather
		// than the current one.
		if remaining(res) == 0 {
			c.log.Debug("anilist quota exhausted, pausing subsequent requests")
			c.pauseFor(time.Minute)
		}

		var parsed response
		if err := json.Unmarshal(raw, &parsed); err != nil {
			return fmt.Errorf("anilist: decode response (HTTP %d): %w", res.StatusCode, err)
		}
		// The HTTP status is derived from the error array, and a partial
		// response can arrive alongside a non-200. Errors decide, not the code.
		if len(parsed.Errors) > 0 {
			return parsed.Errors
		}
		if out == nil {
			return nil
		}
		return json.Unmarshal(parsed.Data, out)
	}
}

func (c *Client) post(ctx context.Context, body []byte) (*http.Response, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	res, err := c.http.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer res.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(res.Body, 32<<20))
	return res, raw, err
}

func (c *Client) sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// The quota is per account, not per connection, so a pause has to apply to
// every goroutine rather than just the one that saw the 429.
func (c *Client) pauseFor(d time.Duration) {
	until := time.Now().Add(d)
	c.mu.Lock()
	defer c.mu.Unlock()
	if until.After(c.pausedUntil) {
		c.pausedUntil = until
	}
}

func (c *Client) awaitPause(ctx context.Context) error {
	c.mu.Lock()
	wait := time.Until(c.pausedUntil)
	c.mu.Unlock()

	if wait <= 0 {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(wait):
		return nil
	}
}

// AniList fires two kinds of 429: the quota limiter sends Retry-After, the burst
// limiter sends no headers at all — so never assume a header is present.
func retryAfter(res *http.Response) time.Duration {
	if v, err := strconv.Atoi(res.Header.Get("Retry-After")); err == nil && v > 0 {
		return time.Duration(v)*time.Second + 500*time.Millisecond
	}
	if v, err := strconv.ParseInt(res.Header.Get("X-RateLimit-Reset"), 10, 64); err == nil {
		if d := time.Until(time.Unix(v, 0)); d > 0 {
			return d + 500*time.Millisecond
		}
	}
	return time.Minute
}

func remaining(res *http.Response) int {
	v, err := strconv.Atoi(res.Header.Get("X-RateLimit-Remaining"))
	if err != nil {
		return -1
	}
	return v
}

func backoff(attempt int) time.Duration {
	return time.Duration(1<<attempt) * time.Second
}
