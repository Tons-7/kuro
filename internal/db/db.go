package db

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"runtime"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

const pragmas = "_pragma=journal_mode(WAL)" +
	"&_pragma=busy_timeout(5000)" +
	"&_pragma=synchronous(NORMAL)" +
	"&_pragma=foreign_keys(1)" +
	"&_pragma=temp_store(MEMORY)" +
	"&_pragma=cache_size(-32000)"

// DB holds separate handles for reads and writes. SQLite permits one writer but
// WAL lets readers run alongside it, so one shared connection would stall the UI.
type DB struct {
	W *sql.DB
	R *sql.DB
}

func Open(path string) (*DB, error) {
	dsn := "file:" + path + "?" + pragmas

	write, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	write.SetMaxOpenConns(1)
	write.SetMaxIdleConns(1)
	if err := write.Ping(); err != nil {
		write.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}

	read, err := sql.Open("sqlite", dsn+"&_pragma=query_only(1)")
	if err != nil {
		write.Close()
		return nil, err
	}
	readers := max(4, runtime.NumCPU())
	read.SetMaxOpenConns(readers)
	// Idle defaults to two, so a burst reopens connections and re-runs every
	// pragma on each one.
	read.SetMaxIdleConns(readers)
	if err := read.Ping(); err != nil {
		write.Close()
		read.Close()
		return nil, fmt.Errorf("open %s for reading: %w", path, err)
	}

	return &DB{W: write, R: read}, nil
}

func (d *DB) Close() error {
	return errors.Join(d.R.Close(), d.W.Close())
}

func (d *DB) Migrate() error {
	if err := prepareGoose(); err != nil {
		return err
	}
	return goose.Up(d.W, "migrations")
}

// MigrateTo stops at a version, so a test can seed data the way an older
// release wrote it and then run the migration that rewrites it.
func (d *DB) MigrateTo(version int64) error {
	if err := prepareGoose(); err != nil {
		return err
	}
	return goose.UpTo(d.W, "migrations", version)
}

func prepareGoose() error {
	goose.SetBaseFS(migrations)
	goose.SetLogger(goose.NopLogger())
	return goose.SetDialect("sqlite3")
}
