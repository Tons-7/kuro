package indexer

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// A search hits every indexer with six title variants and is repeated across
// listing, play and retries; short enough to still find a minutes-old release.
const searchTTL = 3 * time.Minute

type cachedSearch struct {
	results []Torrent
	expires time.Time
}

// Cached wraps a source with a short-lived result cache. Errors are never
// cached: a failed search is usually a timeout, and repeating it is the point.
type Cached struct {
	source Source

	mu      sync.Mutex
	entries map[string]cachedSearch
}

func NewCached(source Source) *Cached {
	return &Cached{source: source, entries: map[string]cachedSearch{}}
}

func (c *Cached) Name() string { return c.source.Name() }

func (c *Cached) Search(ctx context.Context, q Query) ([]Torrent, error) {
	key := fmt.Sprintf("%s\x00%s\x00%d\x00%s\x00%d", q.Text, q.Category, q.Filter, q.Uploader, q.Limit)

	c.mu.Lock()
	entry, ok := c.entries[key]
	if ok && time.Now().After(entry.expires) {
		delete(c.entries, key)
		ok = false
	}
	c.mu.Unlock()
	if ok {
		return entry.results, nil
	}

	results, err := c.source.Search(ctx, q)
	if err != nil {
		return results, err
	}

	c.mu.Lock()
	// A long session browsing many shows would otherwise grow this forever.
	if len(c.entries) > 500 {
		now := time.Now()
		for k, e := range c.entries {
			if now.After(e.expires) {
				delete(c.entries, k)
			}
		}
	}
	c.entries[key] = cachedSearch{results: results, expires: time.Now().Add(searchTTL)}
	c.mu.Unlock()

	return results, nil
}
