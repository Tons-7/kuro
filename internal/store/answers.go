package store

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"time"
)

// AniList's last answer to each read a page made (anilist.Archive), kept in
// the http_cache table the first schema set aside for it. Only served when
// AniList cannot be reached.
const (
	answersMaxAge = 30 * 24 * time.Hour
	answersKept   = 2000
	pruneEvery    = 200
)

func (s *Store) LoadAnswer(ctx context.Context, key string) ([]byte, time.Time, bool) {
	var (
		packed []byte
		at     int64
	)
	if err := s.r.QueryRowContext(ctx,
		`SELECT body, fetched_at FROM http_cache WHERE url = ?`, key).Scan(&packed, &at); err != nil {
		return nil, time.Time{}, false
	}
	zr, err := gzip.NewReader(bytes.NewReader(packed))
	if err != nil {
		return nil, time.Time{}, false
	}
	body, err := io.ReadAll(zr)
	if err != nil {
		return nil, time.Time{}, false
	}
	return body, time.Unix(at, 0), true
}

// SaveAnswer compresses: a page of full records is 150 KB of JSON, a tenth of that packed.
func (s *Store) SaveAnswer(ctx context.Context, key string, body []byte) error {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(body); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	if _, err := s.w.ExecContext(ctx, `
		INSERT INTO http_cache (url, body, fetched_at) VALUES (?, ?, ?)
		ON CONFLICT(url) DO UPDATE SET body = excluded.body, fetched_at = excluded.fetched_at`,
		key, buf.Bytes(), time.Now().Unix()); err != nil {
		return err
	}
	if s.answers.Add(1)%pruneEvery == 0 {
		return s.PruneAnswers(ctx, time.Now().Add(-answersMaxAge), answersKept)
	}
	return nil
}

// PruneAnswers drops answers older than before, then all but the newest keep.
func (s *Store) PruneAnswers(ctx context.Context, before time.Time, keep int) error {
	_, err := s.w.ExecContext(ctx, `
		DELETE FROM http_cache
		WHERE fetched_at < ?1
		   OR url NOT IN (SELECT url FROM http_cache ORDER BY fetched_at DESC LIMIT ?2)`,
		before.Unix(), keep)
	return err
}
