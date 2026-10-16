//go:build !windows

package engine

import "os"

// Unix filesystems leave unwritten stretches as holes without being told.
func markSparse(*os.File) error { return nil }
