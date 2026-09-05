//go:build windows

package library

import (
	"io/fs"
	"syscall"
	"unsafe"
)

var getCompressedFileSize = syscall.NewLazyDLL("kernel32.dll").NewProc("GetCompressedFileSizeW")

// allocatedSize reports what a file costs the volume, which differs from its
// length: sparse downloads cost less, preallocated ones cost full size up front.
func allocatedSize(path string, info fs.FileInfo) int64 {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return info.Size()
	}

	var high uint32
	low, _, _ := getCompressedFileSize.Call(uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&high)))
	if uint32(low) == 0xFFFFFFFF && high == 0 {
		return info.Size()
	}
	return int64(high)<<32 | int64(uint32(low))
}
