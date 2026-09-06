//go:build windows

package proc

import (
	"os/exec"
	"syscall"
	"testing"
)

func TestSetStartAttrsNoWindow(t *testing.T) {
	cmd := exec.Command("java", "-version")
	setStartAttrs(cmd)
	if cmd.SysProcAttr == nil || cmd.SysProcAttr.CreationFlags&createNoWindow == 0 {
		t.Fatal("setStartAttrs must set CREATE_NO_WINDOW")
	}
}

func TestHideConsolePreservesExistingFlags(t *testing.T) {
	cmd := exec.Command("java")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000200} // CREATE_NEW_PROCESS_GROUP
	HideConsole(cmd)
	if cmd.SysProcAttr.CreationFlags&0x00000200 == 0 {
		t.Error("existing CreationFlags lost")
	}
	if cmd.SysProcAttr.CreationFlags&createNoWindow == 0 {
		t.Error("CREATE_NO_WINDOW not set")
	}
}
