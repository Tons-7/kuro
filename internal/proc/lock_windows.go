//go:build windows

package proc

import (
	"os"

	"golang.org/x/sys/windows"
)

// Lock takes an exclusive lock on path for the life of the process; a second kuro on the same data gets
// an error. The port alone did not tell: a listener on every address lets a loopback claim through.
func Lock(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	ol := new(windows.Overlapped)
	if err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, ol); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, ol)
		f.Close()
	}, nil
}

// Alert shows a fatal error in a box: a double-clicked console closes before anyone can read it.
func Alert(title, text string) {
	t, _ := windows.UTF16PtrFromString(title)
	m, _ := windows.UTF16PtrFromString(text)
	windows.MessageBox(0, m, t, windows.MB_OK|windows.MB_ICONERROR)
}
