//go:build !windows

package proc

// Elsewhere the helpers are killed on a clean exit and reparented to init
// otherwise, so there is nothing equivalent to arrange.
func KillChildrenOnExit() error { return nil }
