//go:build !windows

package transcode

import "os"

// openShared is os.Open: elsewhere an open file never blocks its removal.
func openShared(path string) (*os.File, error) { return os.Open(path) }
