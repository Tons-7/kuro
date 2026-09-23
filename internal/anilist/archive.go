package anilist

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

// Archive keeps the last answer AniList gave to each read, for when it gives
// none: a page that errored while AniList was down now shows what it said last.
type Archive interface {
	LoadAnswer(ctx context.Context, key string) (body []byte, at time.Time, ok bool)
	SaveAnswer(ctx context.Context, key string, body []byte) error
}

// SetArchive is separate from New because the store opens after the client.
func (c *Client) SetArchive(a Archive) {
	c.mu.Lock()
	c.archive = a
	c.mu.Unlock()
}

func (c *Client) archiver() Archive {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.archive
}

// After a failure, reads that have a saved answer skip AniList for this long
// instead of each waiting out its own retries.
const downFor = 30 * time.Second

func (c *Client) markDown() {
	c.mu.Lock()
	c.downUntil = time.Now().Add(downFor)
	c.mu.Unlock()
}

func (c *Client) isDown() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Now().Before(c.downUntil)
}

// Not the token: it changes on every login, which would orphan the archive.
// Signed in or not is what changes the answer (adult titles).
func archiveKey(body []byte, authed bool) string {
	sum := sha256.New()
	sum.Write(body)
	if authed {
		sum.Write([]byte{1})
	}
	return hex.EncodeToString(sum.Sum(nil))
}

// unreachable is a failure a saved answer can stand in for. A GraphQL
// rejection is AniList answering, and a cancelled caller wants nothing.
func unreachable(ctx context.Context, err error) bool {
	if err == nil || ctx.Err() != nil {
		return false
	}
	if gql, ok := errors.AsType[Errors](err); ok {
		for _, e := range gql {
			if e.Status >= 500 || e.Status == 403 || e.Status == 429 {
				return true
			}
		}
		return false
	}
	return true
}

type servedKey struct{}

// Served records where a request's answers came from. Only a context carrying
// one may be given a saved answer: background work writes what it reads back
// to the database, and an old answer must never be stored as new.
type Served struct {
	mu    sync.Mutex
	live  bool
	saved time.Time // oldest saved answer used
}

func WithServed(ctx context.Context) (context.Context, *Served) {
	s := &Served{}
	return context.WithValue(ctx, servedKey{}, s), s
}

func servedFrom(ctx context.Context) *Served {
	s, _ := ctx.Value(servedKey{}).(*Served)
	return s
}

// UsedSaved reports whether any answer in this request came from the archive,
// so its results must not be written back as current.
func UsedSaved(ctx context.Context) bool {
	s := servedFrom(ctx)
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.saved.IsZero()
}

// NoteSaved marks a request as answered from saved data kept elsewhere, such as
// the anime row, when AniList could not be reached.
func NoteSaved(ctx context.Context, at time.Time) {
	servedFrom(ctx).noteSaved(at)
}

// Result says whether AniList answered live and, if not, the age of what stood in.
func (s *Served) Result() (live bool, saved time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.live, s.saved
}

func (s *Served) noteLive() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.live = true
	s.mu.Unlock()
}

func (s *Served) noteSaved(at time.Time) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.saved.IsZero() || at.Before(s.saved) {
		s.saved = at
	}
	s.mu.Unlock()
}

// fromArchive answers from the last saved copy, for a request that allows it.
func (c *Client) fromArchive(ctx context.Context, key string, out any) bool {
	served, archive := servedFrom(ctx), c.archiver()
	if served == nil || archive == nil {
		return false
	}
	body, at, ok := archive.LoadAnswer(ctx, key)
	if !ok || decodeInto(body, out) != nil {
		return false
	}
	served.noteSaved(at)
	return true
}
