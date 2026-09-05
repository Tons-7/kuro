//go:build !windows

package proc

// Elsewhere a mount is a mount; there is no cheap equivalent of GetDriveType.
type Drive struct {
	Kind string
	Slow bool
}

func DriveOf(string) Drive { return Drive{Kind: "unknown"} }
