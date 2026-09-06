//go:build windows

package proc

import (
	"golang.org/x/sys/windows"
)

// jobObject groups the process tree. KILL_ON_JOB_CLOSE is deliberately NOT
// set (§19): daemon exit/crash must not take apps down with it; tree kills
// are explicit via TerminateJobObject.
type jobObject struct {
	h windows.Handle
}

func newJobObject() (*jobObject, error) {
	h, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	return &jobObject{h: h}, nil
}

func (j *jobObject) assign(pid int) error {
	ph, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(ph)
	return windows.AssignProcessToJobObject(j.h, ph)
}

func (j *jobObject) close() { _ = windows.CloseHandle(j.h) }
