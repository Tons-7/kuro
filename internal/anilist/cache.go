package anilist

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
	"time"
)

// AniList allows only 30 requests/min app-wide. Airing times live in the data,
// so cached lists go stale only when the schedule is amended, not when one passes.
const (
	cacheTTL     = 10 * time.Minute
	cacheEntries = 250
)

type cached struct {
	body    []byte
	expires time.Time
}

type responseCache struct {
	mu      sync.Mutex
	entries map[string]cached

	// Concurrent identical queries wait on one request rather than each
	// issuing their own: opening the app fires several at once.
	inflight map[string]*sync.WaitGroup
}

func newResponseCache() *responseCache {
	return &responseCache{
		entries:  map[string]cached{},
		inflight: map[string]*sync.WaitGroup{},
	}
}

// Every document here is a compile-time constant, so reading the operation off
// the front is reliable.
func isMutation(query string) bool {
	return strings.HasPrefix(strings.TrimSpace(query), "mutation")
}

// The key covers the account too: AniList hides adult titles from accounts
// that have not opted in, so the same query differs with and without a token.
func cacheKey(query string, body []byte, token string) string {
	sum := sha256.New()
	sum.Write([]byte(query))
	sum.Write([]byte{0})
	sum.Write(body)
	sum.Write([]byte{0})
	sum.Write([]byte(token))
	return hex.EncodeToString(sum.Sum(nil))
}

func (c *responseCache) get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[key]
	if !ok || time.Now().After(entry.expires) {
		return nil, false
	}
	return entry.body, true
}

func (c *responseCache) put(key string, body []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.entries) >= cacheEntries {
		now := time.Now()
		for k, e := range c.entries {
			if now.After(e.expires) {
				delete(c.entries, k)
			}
		}
		// Still full, so drop arbitrary entries: losing one costs a request.
		for k := range c.entries {
			if len(c.entries) < cacheEntries {
				break
			}
			delete(c.entries, k)
		}
	}
	c.entries[key] = cached{body: body, expires: time.Now().Add(cacheTTL)}
}

// enter reports whether this caller should perform the request. When it should
// not, the returned group is the one to wait on before reading the cache again.
func (c *responseCache) enter(key string) (*sync.WaitGroup, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if wg, busy := c.inflight[key]; busy {
		return wg, false
	}
	wg := &sync.WaitGroup{}
	wg.Add(1)
	c.inflight[key] = wg
	return wg, true
}

func (c *responseCache) done(key string, wg *sync.WaitGroup) {
	c.mu.Lock()
	delete(c.inflight, key)
	c.mu.Unlock()
	wg.Done()
}
