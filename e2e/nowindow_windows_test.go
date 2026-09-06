//go:build windows

package e2e

import (
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// consoleWindows counts conhost.exe processes that own a real window
// (MainWindowHandle != 0). CREATE_NO_WINDOW children still get a headless
// conhost, so the window count is the only reliable signal.
func consoleWindows(t *testing.T) int {
	t.Helper()
	script := `Get-Process conhost -ErrorAction SilentlyContinue | Where-Object { $_.MainWindowHandle -ne 0 } | Measure-Object | Select-Object -ExpandProperty Count`
	out, err := exec.Command("powershell", "-NoProfile", "-Command", script).Output()
	if err != nil {
		t.Fatalf("powershell: %v", err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("parse conhost window count %q: %v", out, err)
	}
	return n
}

// TestNoConsoleWindowOnWindows: starting an app from the console-less daemon
// must not open a console window (CREATE_NO_WINDOW).
func TestNoConsoleWindowOnWindows(t *testing.T) {
	if _, err := exec.LookPath("mvn"); err != nil {
		t.Skip("mvn not found in PATH, skipping e2e")
	}
	spm, demoDir := setup(t, "e2e-nowin")

	t.Cleanup(func() {
		if out, code := spm.run("stop", "demo-nowin", "--now"); code != 0 {
			t.Logf("cleanup stop: exit %d\n%s", code, out)
		}
		if out, code := spm.run("kill", "--all"); code != 0 {
			t.Logf("cleanup kill: exit %d\n%s", code, out)
		}
	})

	before := consoleWindows(t)
	spm.mustOK("start", "-f", filepath.Join(demoDir, "spm4a-app.yaml"),
		"--name", "demo-nowin", "--port", "random", "--timeout", "120s")
	app := spm.status("demo-nowin")
	if app.Status != "ready" {
		t.Fatalf("status = %q, want ready", app.Status)
	}
	if after := consoleWindows(t); after != before {
		t.Fatalf("console windows %d -> %d: starting the app opened a console window", before, after)
	}

	spm.mustOK("stop", "demo-nowin")
	spm.mustOK("rm", "demo-nowin")
	spm.mustOK("kill")
}
