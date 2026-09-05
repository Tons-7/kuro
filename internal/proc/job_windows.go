// Package proc ties the lifetime of spawned helpers to this process.
//
// kuro runs rqbit and an ffmpeg per stream. A clean exit stops them, but a crash
// or closing the window does not, and on Windows killing a parent leaves its
// children running — so abrupt exits leak seeding rqbit and resident ffmpeg.
package proc

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// KillChildrenOnExit puts this process in a job object Windows tears down when
// the last handle closes (however it ends); children inherit the job.
func KillChildrenOnExit() error {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return err
	}

	// BREAKAWAY_OK lets a child opt out: helpers die with kuro, but the user's
	// own browser must not get adopted into the job and closed on exit.
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE |
				windows.JOB_OBJECT_LIMIT_BREAKAWAY_OK,
		},
	}
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limits)),
		uint32(unsafe.Sizeof(limits)),
	); err != nil {
		windows.CloseHandle(job)
		return err
	}

	// The handle is deliberately never closed: it has to outlive everything
	// here, and the kernel drops it when the process goes.
	if err := windows.AssignProcessToJobObject(job, windows.CurrentProcess()); err != nil {
		windows.CloseHandle(job)
		return err
	}
	return nil
}
