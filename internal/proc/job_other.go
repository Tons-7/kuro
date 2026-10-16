//go:build !windows

package proc

// Elsewhere helpers die on a clean exit and go to init otherwise: nothing to arrange.
func KillChildrenOnExit() error { return nil }
