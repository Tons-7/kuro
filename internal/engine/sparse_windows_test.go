//go:build windows

package engine

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
	"kuro/internal/bt/metainfo"
	"kuro/internal/bt/storage"
	"syscall"
	"unsafe"
)

// The first write lands at the tail (the prewarm reads the end first): a sparse file costs nothing for
// the stretch before it, where a plain one is zero-filled up to the write.
func TestTailWriteDoesNotFillTheFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ep.mkv")
	const length = 64 << 20
	info := &metainfo.Info{PieceLength: 1 << 20, Length: length, Name: "ep.mkv", Pieces: make([]byte, 20*64)}
	ft := &fileTorrent{info: info, pieces: storage.NewMapPieceCompletion(), files: []segment{{path: path, length: length}}, sparse: map[string]bool{}}
	last := filePiece{ft, info.Piece(63)}
	if _, err := last.WriteAt(make([]byte, 1<<20), 0); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Size() != length {
		t.Fatalf("size = %d, %v", fi.Size(), err)
	}
	p, _ := windows.UTF16PtrFromString(path)
	var high uint32
	low, _, _ := syscall.NewLazyDLL("kernel32.dll").NewProc("GetCompressedFileSizeW").Call(uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&high)))
	if onDisk := int64(high)<<32 | int64(uint32(low)); onDisk > 4<<20 {
		t.Fatalf("%d bytes on disk for a 1 MiB write at the tail", onDisk)
	}
}
