package library

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"kuro/internal/db"
	"kuro/internal/store"
	"kuro/internal/torrent"
)

func restartFixture(t *testing.T, age time.Duration) (*Cache, *store.Store, *fakeRqbit) {
	t.Helper()
	old := forgetAge
	forgetAge = age
	t.Cleanup(func() { forgetAge = old })

	engine := newFakeRqbit()
	srv := httptest.NewServer(engine.handler())
	t.Cleanup(srv.Close)

	conn, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := conn.Migrate(); err != nil {
		t.Fatal(err)
	}
	st := store.New(conn)
	c := NewCache(st, torrent.NewClient(srv.URL), t.TempDir(),
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	return c, st, engine
}

func record(t *testing.T, st *store.Store, hash, name string, fileIndex int) {
	t.Helper()
	ctx := context.Background()
	if err := st.RecordTorrent(ctx, store.TorrentRecord{
		InfoHash: hash, RqbitID: 7, Name: name, FileIndex: fileIndex,
		FilePath: name, TotalSize: 1 << 30,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetCacheBytes(ctx, hash, fileIndex, 1<<29, false); err != nil {
		t.Fatal(err)
	}
}

func passes(t *testing.T, c *Cache, n int) {
	t.Helper()
	for range n {
		if err := c.forgetVanished(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}

func remaining(t *testing.T, st *store.Store) int {
	t.Helper()
	entries, err := st.CacheEntries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}

// An engine listing nothing is one still reloading its session: forgetting
// then would cascade every episode's link to its file away.
func TestEmptyListingNeverForgets(t *testing.T) {
	c, st, _ := restartFixture(t, 0)
	record(t, st, "abc", "Frieren - 05", 0)

	passes(t, c, 3)
	if n := remaining(t, st); n != 1 {
		t.Fatalf("forgotten on empty listings: %d left", n)
	}
}

func TestADownloadTheEngineKeepsNotListingIsForgotten(t *testing.T) {
	c, st, engine := restartFixture(t, 0)
	engine.ids[9] = "other"
	record(t, st, "abc", "Frieren - 05", 0)

	passes(t, c, 1)
	if n := remaining(t, st); n != 1 {
		t.Fatalf("forgotten on the first absence: %d left", n)
	}
	passes(t, c, 1)
	if n := remaining(t, st); n != 0 {
		t.Fatalf("still on record after two absences: %d left", n)
	}
}

// The count is consecutive: a download that comes back is not half-forgotten.
func TestAbsenceCountResetsWhenTheEngineListsItAgain(t *testing.T) {
	c, st, engine := restartFixture(t, 0)
	engine.ids[9] = "other"
	record(t, st, "abc", "held", 0)

	passes(t, c, 1)
	engine.mu.Lock()
	engine.ids[7] = "abc"
	engine.mu.Unlock()
	passes(t, c, 1)
	engine.mu.Lock()
	delete(engine.ids, 7)
	engine.mu.Unlock()
	passes(t, c, 1)

	if n := remaining(t, st); n != 1 {
		t.Fatalf("one absence after it was listed again must not forget it: %d left", n)
	}
}

// A pack has a row per episode; one pass used to count its absence once per row.
func TestAPackCountsOneAbsencePerPass(t *testing.T) {
	c, st, engine := restartFixture(t, 0)
	engine.ids[9] = "other"
	record(t, st, "pack", "Show S01", 1)
	record(t, st, "pack", "Show S01", 2)

	passes(t, c, 1)
	if n := remaining(t, st); n != 2 {
		t.Fatalf("a pack was forgotten in a single pass: %d left", n)
	}
}

func TestForgettingWaitsOutTheAge(t *testing.T) {
	c, st, engine := restartFixture(t, time.Hour)
	engine.ids[9] = "other"
	record(t, st, "abc", "Frieren - 05", 0)

	passes(t, c, 3)
	if n := remaining(t, st); n != 1 {
		t.Fatalf("forgotten before it had been gone long: %d left", n)
	}
}

// The engine losing a kept download says nothing about the file: while it is
// still on disk, it stays listed.
func TestAKeptDownloadStillOnDiskStays(t *testing.T) {
	c, st, engine := restartFixture(t, 0)
	engine.ids[9] = "other"
	record(t, st, "abc", "Frieren - 05.mkv", 0)
	if _, err := st.KeepDownload(context.Background(), "abc", true); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(c.dir, "Frieren - 05.mkv")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	passes(t, c, 3)
	if n := remaining(t, st); n != 1 {
		t.Fatalf("a kept download with its file on disk was forgotten: %d left", n)
	}

	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	passes(t, c, 1)
	if n := remaining(t, st); n != 0 {
		t.Fatalf("its file is gone too, so it should be: %d left", n)
	}
}
