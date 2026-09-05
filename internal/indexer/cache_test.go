package indexer

import (
	"context"
	"errors"
	"testing"
)

type countingSource struct {
	calls int
	err   error
}

func (c *countingSource) Name() string { return "counting" }

func (c *countingSource) Search(context.Context, Query) ([]Torrent, error) {
	c.calls++
	if c.err != nil {
		return nil, c.err
	}
	return []Torrent{{Title: "result", InfoHash: "abc"}}, nil
}

// Opening the sources list and then pressing play searched twice, and every
// failed release searched again. On a slow line each round is seconds.
func TestRepeatedSearchAsksTheSourceOnce(t *testing.T) {
	inner := &countingSource{}
	c := NewCached(inner)
	ctx := context.Background()

	for range 4 {
		got, err := c.Search(ctx, Query{Text: "Frieren 01"})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 {
			t.Fatalf("got %d results, want 1", len(got))
		}
	}
	if inner.calls != 1 {
		t.Errorf("%d searches, want 1", inner.calls)
	}

	if _, err := c.Search(ctx, Query{Text: "Frieren 02"}); err != nil {
		t.Fatal(err)
	}
	if inner.calls != 2 {
		t.Errorf("%d searches after a different query, want 2", inner.calls)
	}
}

// A failed search is usually a timeout, and repeating it is the whole point.
func TestFailuresAreNotCached(t *testing.T) {
	inner := &countingSource{err: errors.New("timeout")}
	c := NewCached(inner)

	for range 3 {
		if _, err := c.Search(context.Background(), Query{Text: "Frieren 01"}); err == nil {
			t.Fatal("expected the error to be reported")
		}
	}
	if inner.calls != 3 {
		t.Errorf("%d searches, want 3: a failure must be retried", inner.calls)
	}
}
