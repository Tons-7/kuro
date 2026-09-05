//go:build windows

package proc

import (
	"fmt"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Drive describes where a directory actually lives. kuro is disk-bound, so a
// folder on a network share or USB stick makes it look slow; a path alone can't
// tell (P:\kuro is an SSD to one person, a wifi share to another).
type Drive struct {
	Kind string // fixed, network, removable, cdrom, ramdisk, unknown
	Slow bool   // worth warning about before anyone blames kuro
}

func DriveOf(dir string) Drive {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Drive{Kind: "unknown"}
	}

	// GetDriveType wants a root: "P:\" or "\\server\share\".
	root := filepath.VolumeName(abs)
	if root == "" {
		return Drive{Kind: "unknown"}
	}
	if !strings.HasSuffix(root, `\`) {
		root += `\`
	}

	p, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return Drive{Kind: "unknown"}
	}

	switch windows.GetDriveType(p) {
	case windows.DRIVE_FIXED:
		// A torrent writes pieces in swarm order — a seek per piece on a spinning
		// disk — while ffmpeg reads the same drive, so the head thrashes and both crawl.
		if spinning(root) {
			return Drive{Kind: "hard drive", Slow: true}
		}
		return Drive{Kind: "fixed"}
	case windows.DRIVE_REMOTE:
		return Drive{Kind: "network", Slow: true}
	case windows.DRIVE_REMOVABLE:
		return Drive{Kind: "removable", Slow: true}
	case windows.DRIVE_CDROM:
		return Drive{Kind: "cdrom", Slow: true}
	case windows.DRIVE_RAMDISK:
		return Drive{Kind: "ramdisk"}
	default:
		return Drive{Kind: "unknown"}
	}
}

// Windows reports a disk's "seek penalty" (its word for mechanical vs SSD).
// Opened with no access rights so no elevation is needed; anything that can't
// answer (RAID, virtual disk) is left alone rather than guessed at.
const (
	ioctlStorageQueryProperty = 0x2D1400
	seekPenaltyProperty       = 7
	standardQuery             = 0
)

type storagePropertyQuery struct {
	PropertyID uint32
	QueryType  uint32
	Additional [1]byte
}

type seekPenaltyDescriptor struct {
	Version           uint32
	Size              uint32
	IncursSeekPenalty byte
	_                 [3]byte
}

func spinning(root string) bool {
	penalty, err := seekPenalty(root)
	return err == nil && penalty
}

// Split out so a test can tell "solid state" from "could not ask" — both leave
// the user unwarned, but only one means the check works.
func seekPenalty(root string) (bool, error) {
	// The volume device, \\.\P:, takes no trailing backslash.
	device := `\\.\` + strings.TrimSuffix(root, `\`)
	name, err := windows.UTF16PtrFromString(device)
	if err != nil {
		return false, err
	}

	h, err := windows.CreateFile(name, 0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return false, err
	}
	defer windows.CloseHandle(h)

	query := storagePropertyQuery{PropertyID: seekPenaltyProperty, QueryType: standardQuery}
	var out seekPenaltyDescriptor
	var returned uint32

	err = windows.DeviceIoControl(h, ioctlStorageQueryProperty,
		(*byte)(unsafe.Pointer(&query)), uint32(unsafe.Sizeof(query)),
		(*byte)(unsafe.Pointer(&out)), uint32(unsafe.Sizeof(out)),
		&returned, nil)
	if err != nil {
		return false, err
	}
	if returned < uint32(unsafe.Sizeof(out)) {
		return false, fmt.Errorf("seek penalty: short reply, %d bytes", returned)
	}
	return out.IncursSeekPenalty != 0, nil
}
