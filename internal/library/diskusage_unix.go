//go:build !windows

package library

import (
	"io/fs"
	"syscall"
)

// allocatedSize reports what a file costs the volume, which differs from its
// length: sparse downloads cost less, preallocated ones cost full size up front.
func allocatedSize(_ string, info fs.FileInfo) int64 {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return info.Size()
	}
	return st.Blocks * 512
}
