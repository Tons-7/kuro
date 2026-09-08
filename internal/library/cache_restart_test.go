package library

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"kuro/internal/db"
	"kuro/internal/store"
	"kuro/internal/torrent"
)

// An engine that lists nothing is what a restart looks like before rqbit has
// reloaded its session. Forgetting then cascades torrent_file away, which is
// the episode's only link to the partly downloaded file.
func TestDownloadsSurviveARestartingEngine(t *testing.T) {
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
	ctx := context.Background()
	if err := st.EnsureAnime(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordTorrent(ctx, store.TorrentRecord{
		InfoHash: "abc", RqbitID: 7, Name: "Frieren - 05", AnimeID: 1, EpKey: "5",
		FileIndex: 0, FilePath: "Frieren - 05.mkv", TotalSize: 1 << 30,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetCacheBytes(ctx, "abc", 0, 1<<29, false); err != nil {
		t.Fatal(err)
	}

	c := NewCache(st, torrent.NewClient(srv.URL), t.TempDir(),
		slog.New(slog.NewTextHandler(io.Discard, nil)))

	if err := c.forgetVanished(ctx); err != nil {
		t.Fatal(err)
	}
	entries, err := st.CacheEntries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("the download was forgotten on the first empty listing: %+v", entries)
	}
	rec, ok, err := st.TorrentForEpisode(ctx, 1, "5")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || rec.InfoHash != "abc" {
		t.Errorf("the episode lost its download: %+v (found %v)", rec, ok)
	}

	// Absent again: now it really is gone.
	if err := c.forgetVanished(ctx); err != nil {
		t.Fatal(err)
	}
	entries, err = st.CacheEntries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("a download the engine keeps not listing should be forgotten: %+v", entries)
	}
}

// The count is consecutive: a download that comes back is not half-forgotten.
func TestAbsenceCountResetsWhenTheEngineListsItAgain(t *testing.T) {
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
	ctx := context.Background()
	if err := st.RecordTorrent(ctx, store.TorrentRecord{
		InfoHash: "abc", RqbitID: 7, Name: "held", FilePath: "held.mkv", TotalSize: 1 << 30,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetCacheBytes(ctx, "abc", 0, 1<<29, false); err != nil {
		t.Fatal(err)
	}

	c := NewCache(st, torrent.NewClient(srv.URL), t.TempDir(),
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := c.forgetVanished(ctx); err != nil {
		t.Fatal(err)
	}

	engine.mu.Lock()
	engine.ids[7] = "abc"
	engine.mu.Unlock()
	if err := c.forgetVanished(ctx); err != nil {
		t.Fatal(err)
	}

	engine.mu.Lock()
	delete(engine.ids, 7)
	engine.mu.Unlock()
	if err := c.forgetVanished(ctx); err != nil {
		t.Fatal(err)
	}

	entries, err := st.CacheEntries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("one absence after it was listed again must not forget it: %+v", entries)
	}
}
