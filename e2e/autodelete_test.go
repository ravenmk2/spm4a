package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// lsJSON parses `spm4a ls --json` output; all=true adds -A.
func (s spmRunner) lsJSON(all bool) []appView {
	s.t.Helper()
	args := []string{"ls", "--json"}
	if all {
		args = append(args, "-A")
	}
	out := s.mustOK(args...)
	var res struct {
		Apps []appView `json:"apps"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		s.t.Fatalf("parse ls output: %v\n%s", err, out)
	}
	return res.Apps
}

// TestEphemeralLifecycle covers the default ephemeral semantics: stop removes
// the record; daemon restarts adopt the process like any other (ephemeral
// only governs record retention at stop time); and the explicit
// --ephemeral=false opt-out keeps the record after stop.
func TestEphemeralLifecycle(t *testing.T) {
	if _, err := exec.LookPath("mvn"); err != nil {
		t.Skip("mvn not found in PATH, skipping e2e")
	}
	spm, _ := setup(t, "e2e-ephemeral")

	t.Cleanup(func() {
		spm.run("stop", "demo-eph", "--now")
		spm.run("stop", "demo-keep", "--now")
		spm.run("kill", "--all")
	})

	// default: ephemeral — stop deletes the record
	spm.mustOK("start", "--name", "demo-eph", "--port", "random", "--timeout", "120s")
	if app := spm.status("demo-eph"); !app.Spec.Ephemeral {
		t.Fatalf("spec.ephemeral = false, want true (default)")
	}
	spm.mustOK("stop", "demo-eph")
	if apps := spm.lsJSON(false); len(apps) != 0 {
		t.Fatalf("ls after stop = %d apps, want 0 (ephemeral default)", len(apps))
	}
	if out, code := spm.run("rm", "demo-eph"); code != 3 {
		t.Fatalf("rm after ephemeral stop exited %d, want 3\n%s", code, out)
	}

	// daemon restart: ephemeral apps are adopted like any other — ephemeral
	// only governs record retention at stop time, never process management
	spm.mustOK("start", "--name", "demo-eph", "--port", "random", "--timeout", "120s")
	pidBefore := spm.status("demo-eph").PID
	spm.mustOK("kill")
	waitFor(t, 10*time.Second, func() bool {
		_, err := os.Stat(filepath.Join(os.Getenv("SPM4A_HOME"), "run", "daemon.json"))
		return err != nil
	}, "daemon.json removed (daemon exited)")
	if !pidAlive(pidBefore) {
		t.Fatalf("app pid %d died with the daemon", pidBefore)
	}
	after := spm.status("demo-eph") // any CLI command respawns the daemon, which adopts
	if after.PID != pidBefore {
		t.Fatalf("adopted PID = %d, want same process %d", after.PID, pidBefore)
	}
	// ...but stop still deletes the ephemeral record
	spm.mustOK("stop", "demo-eph")
	if apps := spm.lsJSON(false); len(apps) != 0 {
		t.Fatalf("ls after stop (adopted ephemeral) = %d apps, want 0", len(apps))
	}

	// dead ephemeral records (crashed while the daemon was down or before)
	// are dropped on daemon restart
	spm.mustOK("start", "--name", "demo-eph", "--port", "random", "--timeout", "120s")
	killPidTree(spm.status("demo-eph").PID) // simulate a crash
	waitFor(t, 10*time.Second, func() bool {
		return spm.status("demo-eph").Status == "error"
	}, "app marked error after crash")
	spm.mustOK("kill")
	waitFor(t, 10*time.Second, func() bool {
		_, err := os.Stat(filepath.Join(os.Getenv("SPM4A_HOME"), "run", "daemon.json"))
		return err != nil
	}, "daemon.json removed (daemon exited)")
	if apps := spm.lsJSON(false); len(apps) != 0 {
		t.Fatalf("ls after restart with dead ephemeral = %d apps, want 0", len(apps))
	}

	// explicit opt-out: --ephemeral=false keeps the record after stop
	spm.mustOK("start", "--name", "demo-keep", "--ephemeral=false", "--port", "random", "--timeout", "120s")
	spm.mustOK("stop", "demo-keep")
	if app := spm.status("demo-keep"); app.Status != "stopped" {
		t.Fatalf("demo-keep status = %q, want stopped (record kept)", app.Status)
	}
	spm.mustOK("rm", "demo-keep")
}

// TestStopRmAll: stop --all / rm --all operate on the current namespace,
// and with -A across all namespaces. Apps are non-ephemeral so records
// survive the stop and rm --all has something to remove.
func TestStopRmAll(t *testing.T) {
	if _, err := exec.LookPath("mvn"); err != nil {
		t.Skip("mvn not found in PATH, skipping e2e")
	}
	spm, _ := setup(t, "e2e-batch")

	t.Cleanup(func() {
		spm.run("stop", "--all", "-A", "--now")
		spm.run("kill", "--all")
	})

	spm.mustOK("start", "--name", "demo-a", "--ephemeral=false", "--port", "random", "--timeout", "120s")
	spm.mustOK("start", "--name", "demo-b", "--ephemeral=false", "--port", "random", "--timeout", "120s")
	spm.mustOK("start", "--namespace", "e2e-batch-b", "--name", "demo-c", "--ephemeral=false",
		"--port", "random", "--timeout", "120s")

	// --all rejects a positional name
	if out, code := spm.run("stop", "demo-a", "--all"); code != 2 {
		t.Fatalf("stop <name> --all exited %d, want 2\n%s", code, out)
	}

	// current namespace only: demo-c keeps running
	out := spm.mustOK("stop", "--all")
	if !strings.Contains(out, "demo-a") || !strings.Contains(out, "demo-b") {
		t.Fatalf("stop --all output missing apps:\n%s", out)
	}
	var resC struct {
		App appView `json:"app"`
	}
	outC := spm.mustOK("status", "demo-c", "--namespace", "e2e-batch-b", "--json")
	if err := json.Unmarshal([]byte(outC), &resC); err != nil {
		t.Fatalf("parse status output: %v\n%s", err, outC)
	}
	if resC.App.Status != "ready" {
		t.Fatalf("demo-c status = %q, want ready (other namespace untouched)", resC.App.Status)
	}

	// -A widens the scope to every namespace
	spm.mustOK("stop", "--all", "-A")
	out = spm.mustOK("rm", "--all", "-A")
	if !strings.Contains(out, "removed") {
		t.Fatalf("rm --all output:\n%s", out)
	}
	if apps := spm.lsJSON(true); len(apps) != 0 {
		t.Fatalf("ls -A after rm --all = %d apps, want 0", len(apps))
	}
}
