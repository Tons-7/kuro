package store

import "context"

// Checkpoint folds the WAL into the database file, so a copy of kuro.db alone
// is complete.
func (s *Store) Checkpoint(ctx context.Context) error {
	_, err := s.w.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
	return err
}
