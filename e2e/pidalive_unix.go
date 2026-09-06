//go:build !windows

package e2e

import "syscall"

func pidAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// killPidTree force-kills a process and its children (orphan cleanup).
// Children share the pgid of the root, so signal the group first.
func killPidTree(pid int) {
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	_ = syscall.Kill(pid, syscall.SIGKILL)
}
