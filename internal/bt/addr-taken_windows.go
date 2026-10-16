package bt

import (
	"errors"

	"golang.org/x/sys/windows"
)

// addrTakenOS: Windows' own "in use", or a port in a range it keeps back (a random port's UDP twin can be).
func addrTakenOS(err error) bool {
	return errors.Is(err, windows.WSAEADDRINUSE) || errors.Is(err, windows.WSAEACCES)
}
