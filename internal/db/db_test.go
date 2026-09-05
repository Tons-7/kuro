package db

import (
	"path/filepath"
	"runtime"
	"testing"
)

func open(t *testing.T) *DB {
	t.Helper()
	conn, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func migrated(t *testing.T) *DB {
	t.Helper()
	conn := open(t)
	if err := conn.Migrate(); err != nil {
		t.Fatal(err)
	}
	return conn
}

func TestOpenAppliesPragmas(t *testing.T) {
	conn := open(t)

	tests := []struct {
		pragma string
		want   string
	}{
		{"journal_mode", "wal"},
		{"foreign_keys", "1"},
		{"busy_timeout", "5000"},
		{"synchronous", "1"},
	}

	for _, tt := range tests {
		var got string
		if err := conn.W.QueryRow("PRAGMA " + tt.pragma).Scan(&got); err != nil {
			t.Fatalf("%s: %v", tt.pragma, err)
		}
		if got != tt.want {
			t.Errorf("%s = %q, want %q", tt.pragma, got, tt.want)
		}
	}
}

// SQLite permits one writer, but WAL lets readers run alongside it.
func TestConnectionPoolShape(t *testing.T) {
	conn := open(t)

	if got := conn.W.Stats().MaxOpenConnections; got != 1 {
		t.Errorf("writer pool = %d, want 1", got)
	}
	if got := conn.R.Stats().MaxOpenConnections; got < 4 || got < runtime.NumCPU() {
		t.Errorf("reader pool = %d, want at least max(4, NumCPU)", got)
	}
}

// query_only makes the split structural: a write routed to the read handle
// fails instead of silently contending with the writer.
func TestReadHandleRejectsWrites(t *testing.T) {
	conn := migrated(t)

	_, err := conn.R.Exec(`INSERT INTO setting (key, value) VALUES ('x', 'y')`)
	if err == nil {
		t.Fatal("the read handle accepted a write")
	}

	if _, err := conn.W.Exec(`INSERT INTO setting (key, value) VALUES ('x', 'y')`); err != nil {
		t.Fatalf("the write handle rejected a write: %v", err)
	}
}

func TestReadHandleSeesCommittedWrites(t *testing.T) {
	conn := migrated(t)

	if _, err := conn.W.Exec(`INSERT INTO setting (key, value) VALUES ('k', 'v')`); err != nil {
		t.Fatal(err)
	}

	var got string
	if err := conn.R.QueryRow(`SELECT value FROM setting WHERE key = 'k'`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != "v" {
		t.Fatalf("read handle returned %q", got)
	}
}

func TestOpenRejectsUnwritablePath(t *testing.T) {
	if _, err := Open(filepath.Join(t.TempDir(), "missing", "dir", "x.db")); err == nil {
		t.Fatal("expected an error for a path whose parent does not exist")
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	conn := open(t)

	for i := range 3 {
		if err := conn.Migrate(); err != nil {
			t.Fatalf("run %d: %v", i+1, err)
		}
	}

	// Repeated runs must not re-apply anything, whatever the latest version is.
	var applied, version int
	if err := conn.R.QueryRow(
		`SELECT count(*), coalesce(max(version_id), 0) FROM goose_db_version WHERE version_id > 0`,
	).Scan(&applied, &version); err != nil {
		t.Fatal(err)
	}
	if applied != version {
		t.Fatalf("%d rows for %d migrations; a migration was applied twice", applied, version)
	}
}

func TestMigrateCreatesSchema(t *testing.T) {
	conn := migrated(t)

	want := []string{
		"anime", "anime_fts", "episode", "filler", "skip_time", "list_entry",
		"playback", "watch_session", "torrent", "torrent_file", "cache_entry",
		"release", "seadex", "follow", "job", "setting", "home_widget", "http_cache",
	}
	for _, table := range want {
		var name string
		if err := conn.R.QueryRow(`SELECT name FROM sqlite_master WHERE name = ?`, table).Scan(&name); err != nil {
			t.Errorf("table %q missing: %v", table, err)
		}
	}
}

// STRICT tables reject a type mismatch instead of coercing it silently.
func TestStrictTablesRejectWrongTypes(t *testing.T) {
	conn := migrated(t)

	_, err := conn.W.Exec(
		`INSERT INTO anime (id, title_romaji, synced_at) VALUES (?, ?, ?)`,
		1, "Frieren", "not-a-number")
	if err == nil {
		t.Fatal("a text value was accepted into an INTEGER column")
	}
}

func TestForeignKeysAreEnforced(t *testing.T) {
	conn := migrated(t)

	_, err := conn.W.Exec(`INSERT INTO list_entry (id, anime_id, local_updated_at) VALUES (1, 999, 0)`)
	if err == nil {
		t.Fatal("an entry referencing a missing anime was accepted")
	}
}

func TestCloseReleasesBothHandles(t *testing.T) {
	conn, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}

	if err := conn.W.Ping(); err == nil {
		t.Error("write handle still open")
	}
	if err := conn.R.Ping(); err == nil {
		t.Error("read handle still open")
	}
}
