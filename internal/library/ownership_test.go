package library

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"kuro/internal/db"
	"kuro/internal/indexer"
	"kuro/internal/parse"
	"kuro/internal/store"
	"kuro/internal/torrent"
)

func ownedFixture(t *testing.T) (*Cache, *store.Store, *fakeRqbit) {
	t.Helper()
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
	c := NewCache(st, torrent.NewClient(srv.URL), t.TempDir(), discard())
	engine.folder = c.dir
	return c, st, engine
}

// Another install's (or program's) downloads in a shared engine would be
// adopted into the budget and swept away.
func TestAdoptSkipsDownloadsOutsideTheCache(t *testing.T) {
	c, st, engine := ownedFixture(t)
	engine.ids[1] = "foreign"
	engine.folder = t.TempDir()

	if err := c.adopt(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := remaining(t, st); n != 0 {
		t.Fatalf("adopted %d download(s) from another folder", n)
	}

	engine.folder = c.dir
	if err := c.adopt(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := remaining(t, st); n != 1 {
		t.Fatalf("our own untracked download was not adopted: %d", n)
	}
}

func TestEvictNeverDeletesOutsideTheCache(t *testing.T) {
	c, st, engine := ownedFixture(t)
	record(t, st, "abc", "abc", 0)
	engine.ids[1] = "abc"
	engine.folder = t.TempDir()

	if err := c.evict(context.Background(), store.CacheEntry{InfoHash: "abc"}); err != nil {
		t.Fatal(err)
	}
	if engine.wasDeleted("abc") {
		t.Error("deleted a download outside kuro's cache folder")
	}
}

// Ids are per engine session: the stored one can name another torrent by now.
func TestEvictFindsTheTorrentByHash(t *testing.T) {
	c, st, engine := ownedFixture(t)
	record(t, st, "abc", "abc", 0) // recorded as id 7
	engine.ids[3] = "abc"
	engine.ids[7] = "other"

	if err := c.evict(context.Background(), store.CacheEntry{InfoHash: "abc"}); err != nil {
		t.Fatal(err)
	}
	if !engine.wasDeleted("abc") || engine.wasDeleted("other") {
		t.Errorf("deleted %v; want abc only", engine.deleted)
	}
}

func TestOrphansKeepsEverythingOnRecord(t *testing.T) {
	c, st, engine := ownedFixture(t)
	was := orphanSettle
	orphanSettle = 0
	t.Cleanup(func() { orphanSettle = was })

	record(t, st, "rec", "recorded.mkv", 0)
	engine.ids[1] = "live.mkv"
	for _, name := range []string{"recorded.mkv", "live.mkv", "stray.mkv"} {
		if err := os.WriteFile(filepath.Join(c.dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(c.dir, SessionDirName), 0o755); err != nil {
		t.Fatal(err)
	}

	preview, err := c.Orphans(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview) != 1 || preview[0].Name != "stray.mkv" {
		t.Fatalf("preview = %+v, want only stray.mkv", preview)
	}
	if _, err := os.Stat(filepath.Join(c.dir, "stray.mkv")); err != nil {
		t.Fatal("a preview deleted something")
	}

	if _, err := c.Orphans(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]bool{"recorded.mkv": true, "live.mkv": true, "stray.mkv": false, SessionDirName: true} {
		_, err := os.Stat(filepath.Join(c.dir, name))
		if (err == nil) != want {
			t.Errorf("%s present = %v, want %v", name, err == nil, want)
		}
	}
}

// Right after a restart rqbit lists nothing yet; everything would look orphaned.
func TestOrphansRefusesWhileTheEngineIsLoading(t *testing.T) {
	c, st, _ := ownedFixture(t)
	record(t, st, "rec", "recorded.mkv", 0)
	file := filepath.Join(c.dir, "recorded.mkv")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := c.Orphans(context.Background(), false); !errors.Is(err, ErrEngineLoading) {
		t.Fatalf("err = %v, want ErrEngineLoading", err)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatal("deleted while the engine was loading")
	}
}

func TestClearKeepsKeptDownloads(t *testing.T) {
	c, st := newCache(t)
	ctx := context.Background()
	cache(t, st, "kept", 1, 1<<30, false)
	cache(t, st, "cached", 2, 1<<30, false)
	if _, err := st.KeepDownload(ctx, "kept", true); err != nil {
		t.Fatal(err)
	}

	for _, completedOnly := range []bool{true, false} {
		if _, _, err := c.Clear(ctx, completedOnly); err != nil {
			t.Fatal(err)
		}
	}
	left, _ := st.CacheEntries(ctx)
	if len(left) != 1 || left[0].InfoHash != "kept" {
		t.Fatalf("remaining = %+v, want only the kept download", left)
	}
}

// Clearing deletes whole torrents, so a pack with one file still arriving is
// not finished.
func TestClearFinishedOnlyJudgesTheWholeTorrent(t *testing.T) {
	c, st := newCache(t)
	ctx := context.Background()
	cacheState(t, st, "pack", 1, 1<<30, false, true)
	cacheState(t, st, "pack", 2, 1<<30, false, false)

	if _, _, err := c.Clear(ctx, true); err != nil {
		t.Fatal(err)
	}
	if n := remaining(t, st); n != 2 {
		t.Fatalf("a part-downloaded pack was cleared: %d rows left", n)
	}
}

// "Overlord IV" states no season in words; its S04 releases were all refused
// as season 1 asked for.
func TestNumeralSequelTakesItsSeason(t *testing.T) {
	st := prefetchStore(t)
	ctx := context.Background()
	eps := 13
	if _, err := st.ImportList(ctx, []store.Anime{{ID: 1, Romaji: "Overlord IV", Synonyms: "[]", Genres: "[]", Episodes: &eps}}, nil, store.ImportMerge); err != nil {
		t.Fatal(err)
	}
	f := NewFinder(st, fixedIndexer{}, discard())
	titles, _ := st.SearchTitles(ctx, 1)
	req := f.numbering(ctx, Request{AnimeID: 1, Episode: 5}, titles, "")
	if req.Season != 4 {
		t.Fatalf("season = %d, want 4", req.Season)
	}
	for _, name := range []string{"[Judas] Overlord IV - S04E05 [1080p]", "Overlord.S04E05.1080p.WEB.H264-SENPAI"} {
		if !verifies(parse.Parse(name), req) {
			t.Errorf("refused %s", name)
		}
	}
}

type downIndexer struct{}

func (downIndexer) Name() string { return "down" }
func (downIndexer) Search(context.Context, indexer.Query) ([]indexer.Torrent, error) {
	return nil, errors.New("dial tcp: no such host")
}

// Every site failing is an outage; reporting "no release" sent people looking
// for a release that may well exist.
func TestFindReportsAnOutage(t *testing.T) {
	st := prefetchStore(t)
	ctx := context.Background()
	eps := 12
	if _, err := st.ImportList(ctx, []store.Anime{{ID: 1, Romaji: "Show", Synonyms: "[]", Genres: "[]", Episodes: &eps}}, nil, store.ImportMerge); err != nil {
		t.Fatal(err)
	}
	f := NewFinder(st, downIndexer{}, discard())
	if _, err := f.Find(ctx, Request{AnimeID: 1, Episode: 1, Season: 1}); err == nil {
		t.Fatal("every search failed, yet Find reported an empty result")
	}
}

// A candidate that turns out to be a download already on record is someone's,
// whatever the race assumed.
func TestDiscardSparesADownloadOnRecord(t *testing.T) {
	engine := newFakeRqbit()
	p := newPlayback(t, engine, nil)
	if err := p.store.RecordTorrent(context.Background(), store.TorrentRecord{
		InfoHash: goodHash, RqbitID: 1, Name: "kept", FilePath: "kept.mkv", TotalSize: 1,
	}); err != nil {
		t.Fatal(err)
	}
	engine.ids[1] = goodHash
	engine.ids[2] = otherHash

	p.discard(context.Background(), 1, goodHash)
	p.discard(context.Background(), 2, otherHash)
	if engine.wasDeleted(goodHash) {
		t.Error("discarded a download on record")
	}
	if !engine.wasDeleted(otherHash) {
		t.Error("an abandoned candidate was left")
	}
}
