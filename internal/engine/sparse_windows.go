//go:build windows

package engine

import (
	"os"

	"golang.org/x/sys/windows"
)

// markSparse keeps NTFS from zero-filling a file up to the first write: the prewarm writes the tail first.
func markSparse(f *os.File) error {
	var returned uint32
	return windows.DeviceIoControl(windows.Handle(f.Fd()), windows.FSCTL_SET_SPARSE, nil, 0, nil, 0, &returned, nil)
}
