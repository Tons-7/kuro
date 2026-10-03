-- +goose Up

-- Devices that asked the host for access; id is a hash of the device's cookie.
CREATE TABLE access_device (
    id           TEXT PRIMARY KEY,
    name         TEXT NOT NULL,
    addr         TEXT NOT NULL,
    status       TEXT NOT NULL CHECK (status IN ('pending', 'approved', 'denied')),
    requested_at INTEGER NOT NULL,
    decided_at   INTEGER NOT NULL DEFAULT 0
);

-- +goose Down
DROP TABLE access_device;
