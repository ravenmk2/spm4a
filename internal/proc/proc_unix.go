//go:build !windows

package proc

import (
	"errors"
	"os/exec"
	"syscall"
)

// On Unix the tree is managed via the process group (Setpgid, pgid == pid).
type treeHandle struct{}

func setStartAttrs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// HideConsole is a no-op on Unix (no console window concept).
func HideConsole(*exec.Cmd) {}

func newTreeHandle(*exec.Cmd) treeHandle { return treeHandle{} }

func (t treeHandle) terminate(pid int) error { return syscall.Kill(-pid, syscall.SIGTERM) }

func (t treeHandle) kill(pid int) error { return syscall.Kill(-pid, syscall.SIGKILL) }

func (t treeHandle) close() {}

func terminatePID(pid int) error { return syscall.Kill(-pid, syscall.SIGTERM) }

func killPID(pid int) { _ = syscall.Kill(-pid, syscall.SIGKILL) }

func Alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
