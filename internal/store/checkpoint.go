package store

import "context"

// SnapshotTo writes a consistent copy of the database, WAL included, in one
// statement. Copying kuro.db after a checkpoint misses commits a busy reader
// kept in the WAL.
func (s *Store) SnapshotTo(ctx context.Context, path string) error {
	_, err := s.w.ExecContext(ctx, `VACUUM INTO ?`, path)
	return err
}
