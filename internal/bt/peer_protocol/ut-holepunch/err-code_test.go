package utHolepunch

import (
	"math/rand"
	"testing"
)

func TestUnknownErrCodeError(t *testing.T) {
	if ErrCode(rand.Uint32()).Error() == "" {
		t.Error("an unknown error code has no text")
	}
}
