package engine

import (
	"context"
	"testing"
	"time"
)

// A file downloaded whole is one stretch from start to end; one never started has none.
func TestFileRangesFollowTheDownload(t *testing.T) {
	f := newFixture(t)
	c := ctx(t)
	in, err := f.client.Inspect(c, f.magnet)
	if err != nil {
		t.Fatal(err)
	}
	added, err := f.client.Add(c, f.magnet, in.Details.Files[1])
	if err != nil {
		t.Fatal(err)
	}
	if err := f.client.Await(c, added.ID, 20*time.Second); err != nil {
		t.Fatal(err)
	}

	got, err := f.client.Ranges(c, added.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	want := int64(len(f.content["Show - 02.mkv"]))
	if got.Length != want || len(got.Ranges) != 1 || got.Ranges[0].From != 0 || got.Ranges[0].To != want {
		t.Fatalf("ranges = %+v, want one stretch of %d bytes", got, want)
	}

	// Episode 1 shares its last piece with episode 2, so that sliver of it is on disk and nothing before.
	other, err := f.client.Ranges(c, added.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(other.Ranges) != 1 || other.Ranges[0].From == 0 || other.Ranges[0].To != other.Length {
		t.Fatalf("the neighbour's stretches = %+v, want only the shared tail", other)
	}
	if _, err := f.client.Ranges(context.Background(), added.ID, 9); err == nil {
		t.Error("a file that does not exist answered")
	}
}
