//go:build windows

package proc

import "testing"

func TestDriveOfClassifiesRealPaths(t *testing.T) {
	if got := DriveOf("C:\\Windows"); got.Kind != "fixed" || got.Slow {
		t.Errorf("C:\\Windows = %+v, want a fixed disk nobody is warned about", got)
	}
	// A UNC path is remote whether or not the share is reachable.
	if got := DriveOf("\\\\localhost\\C$\\Windows"); !got.Slow {
		t.Errorf("UNC path = %+v, want it flagged slow", got)
	}
	// A letter with nothing mounted must not be reported as a problem.
	if got := DriveOf("Q:\\nothing"); got.Slow {
		t.Errorf("unmounted letter = %+v, want no warning", got)
	}
}
