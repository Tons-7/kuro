package store

import (
	"context"
	"time"
)

const (
	DevicePending  = "pending"
	DeviceApproved = "approved"
	DeviceDenied   = "denied"
)

// Device is a phone, TV or browser that asked the host for access.
type Device struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Addr        string `json:"addr"`
	Status      string `json:"status"`
	RequestedAt int64  `json:"requestedAt"`
	DecidedAt   int64  `json:"decidedAt"`
}

func (s *Store) Devices(ctx context.Context) ([]Device, error) {
	rows, err := s.r.QueryContext(ctx, `
		SELECT id, name, addr, status, requested_at, decided_at
		FROM access_device ORDER BY status = 'pending' DESC, requested_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Device{}
	for rows.Next() {
		var d Device
		if err := rows.Scan(&d.ID, &d.Name, &d.Addr, &d.Status, &d.RequestedAt, &d.DecidedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// RequestAccess records a device as waiting, new or asking again.
func (s *Store) RequestAccess(ctx context.Context, id, name, addr string) error {
	_, err := s.w.ExecContext(ctx, `
		INSERT INTO access_device (id, name, addr, status, requested_at) VALUES (?, ?, ?, 'pending', ?)
		ON CONFLICT (id) DO UPDATE SET
			name = excluded.name, addr = excluded.addr, status = 'pending', requested_at = excluded.requested_at`,
		id, name, addr, time.Now().Unix())
	return err
}

func (s *Store) DecideDevice(ctx context.Context, id, status string) error {
	_, err := s.w.ExecContext(ctx,
		`UPDATE access_device SET status = ?, decided_at = ? WHERE id = ?`, status, time.Now().Unix(), id)
	return err
}

func (s *Store) DeleteDevice(ctx context.Context, id string) error {
	_, err := s.w.ExecContext(ctx, `DELETE FROM access_device WHERE id = ?`, id)
	return err
}
