//go:build !windows

package config

import "os/exec"

// Only Windows ties children to the parent's lifetime; elsewhere init adopts them.
func detach(cmd *exec.Cmd) {}

func Detach(cmd *exec.Cmd) { detach(cmd) }
