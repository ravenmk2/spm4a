//go:build windows

package proc

import (
	"os/exec"
	"sync"

	"golang.org/x/sys/windows"
)

// On Windows the tree is managed via a Job Object with
// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE: children spawned by the root (build
// tool JVM -> forked app JVM) stay in the job and die together.
type treeHandle struct {
	mu  sync.Mutex
	job windows.Handle
}

func setStartAttrs(*exec.Cmd) {}

func newTreeHandle(cmd *exec.Cmd) treeHandle {
	job, err := newJobObject()
	if err != nil {
		return treeHandle{}
	}
	if err := job.assign(cmd.Process.Pid); err != nil {
		job.close()
		return treeHandle{}
	}
	return treeHandle{job: job.h}
}

func (t *treeHandle) jobHandle() windows.Handle {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.job
}

// terminate hard-kills the whole tree; there is no graceful signal on Windows.
func (t *treeHandle) terminate(pid int) error { return t.kill(pid) }

func (t *treeHandle) kill(pid int) error {
	if h := t.jobHandle(); h != 0 {
		return windows.TerminateJobObject(h, 1)
	}
	return terminatePID(pid)
}

func (t *treeHandle) close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.job != 0 {
		_ = windows.CloseHandle(t.job)
		t.job = 0
	}
}

func terminatePID(pid int) error {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(h)
	return windows.TerminateProcess(h, 1)
}

func killPID(pid int) { _ = terminatePID(pid) }

func Alive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == stillActive
}

const stillActive = 259
