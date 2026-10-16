//go:build !wasm

package bt

import "syscall"

var lingerOffVal = syscall.Linger{
	Onoff:  0,
	Linger: 0,
}
