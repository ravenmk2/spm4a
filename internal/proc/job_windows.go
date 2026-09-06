//go:build windows

package proc

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

type jobObject struct {
	h windows.Handle
}

func newJobObject() (*jobObject, error) {
	h, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	_, err = windows.SetInformationJobObject(
		h, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)))
	if err != nil {
		windows.CloseHandle(h)
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
