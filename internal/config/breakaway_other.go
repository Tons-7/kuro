//go:build !windows

package config

import "os/exec"

// Only Windows ties children to the parent's lifetime; elsewhere they are
// reparented to init and need nothing.
func detach(cmd *exec.Cmd) {}

func Detach(cmd *exec.Cmd) { detach(cmd) }
