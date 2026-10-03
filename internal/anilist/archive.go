package anilist

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

// Archive keeps AniList's last answer to each read, served when it is down.
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

// After a failure, saved answers are served without retrying for this long.
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

// Keyed on signed-in or not (adult titles), not the token, which changes per login.
func archiveKey(body []byte, authed bool) string {
	sum := sha256.New()
	sum.Write(body)
	if authed {
		sum.Write([]byte{1})
	}
	return hex.EncodeToString(sum.Sum(nil))
}

// unreachable: AniList gave no answer. A GraphQL rejection is an answer.
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
// one gets saved answers; background work would store them as new.
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

// UsedSaved: some answer was a saved one, so nothing may be written back.
func UsedSaved(ctx context.Context) bool {
	s := servedFrom(ctx)
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.saved.IsZero()
}

// NoteSaved marks a request answered from data saved elsewhere (the anime row).
func NoteSaved(ctx context.Context, at time.Time) {
	servedFrom(ctx).noteSaved(at)
}

// Result: answered live, and the oldest saved answer used.
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
