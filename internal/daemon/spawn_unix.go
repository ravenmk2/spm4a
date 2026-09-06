//go:build !windows

package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"spm4a/internal/ipc"
)

func spawnDaemon(ep ipc.Endpoint) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	logf, err := os.OpenFile(ep.DaemonLogPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open daemon log: %w", err)
	}
	defer logf.Close()
	cmd := exec.Command(exe, "daemon")
	cmd.Dir = ep.Home
	cmd.Stdout = logf
	cmd.Stderr = logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
