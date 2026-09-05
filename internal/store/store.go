// Package store owns every SQL statement in the application. Other packages
// depend on its domain types, never on database/sql.
package store

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"

	"kuro/internal/db"
)

type Store struct {
	r *sql.DB
	w *sql.DB
}

func New(d *db.DB) *Store { return &Store{r: d.R, w: d.W} }

func (s *Store) Setting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.r.QueryRowContext(ctx, `SELECT value FROM setting WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

func (s *Store) SettingInt(ctx context.Context, key string) (int, error) {
	v, err := s.Setting(ctx, key)
	if err != nil || v == "" {
		return 0, err
	}
	return strconv.Atoi(v)
}

func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	return s.SetSettings(ctx, map[string]string{key: value})
}

func (s *Store) SetSettings(ctx context.Context, kv map[string]string) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for k, v := range kv {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO setting (key, value) VALUES (?, ?)
			 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, k, v)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// EnsureSetting writes value only when the key is unset, returning whatever
// ends up stored. Two callers racing on first run agree on one result.
func (s *Store) EnsureSetting(ctx context.Context, key, value string) (string, error) {
	if _, err := s.w.ExecContext(ctx,
		`INSERT INTO setting (key, value) VALUES (?, ?) ON CONFLICT(key) DO NOTHING`,
		key, value); err != nil {
		return "", err
	}
	return s.Setting(ctx, key)
}

// Secret reports whether a setting is a credential. These are writable but never
// readable back, so the settings endpoint can't hand out the AniList token.
// Suffix matched without a separator so it covers "client_secret" too.
func Secret(key string) bool {
	switch {
	case strings.HasSuffix(key, ".token"),
		strings.HasSuffix(key, "secret"),
		key == "anilist.state":
		return true
	}
	return false
}

// Settings omits credentials. Use Setting for those, by key.
func (s *Store) Settings(ctx context.Context) (map[string]string, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT key, value FROM setting ORDER BY key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		if !Secret(k) {
			out[k] = v
		}
	}
	return out, rows.Err()
}

func (s *Store) CountSettings(ctx context.Context) (int, error) {
	var n int
	err := s.r.QueryRowContext(ctx, `SELECT count(*) FROM setting`).Scan(&n)
	return n, err
}
