package store

import "context"

// SnapshotTo writes a consistent copy, WAL included; copying kuro.db can miss commits still in the WAL.
func (s *Store) SnapshotTo(ctx context.Context, path string) error {
	_, err := s.w.ExecContext(ctx, `VACUUM INTO ?`, path)
	return err
}
