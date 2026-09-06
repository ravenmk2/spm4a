package e2e

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestCustomLauncher runs the demo jar as a plain custom command; the
// SPRING_APPLICATION_JSON env injection must still drive the port.
func TestCustomLauncher(t *testing.T) {
	if _, err := exec.LookPath("mvn"); err != nil {
		t.Skip("mvn not found in PATH, skipping e2e")
	}
	spm, demoDir := setup(t, "e2e-custom")

	t.Cleanup(func() {
		if out, code := spm.run("stop", "demo-custom", "--now"); code != 0 {
			t.Logf("cleanup stop: exit %d\n%s", code, out)
		}
		if out, code := spm.run("kill", "--all"); code != 0 {
			t.Logf("cleanup kill: exit %d\n%s", code, out)
		}
	})

	jarPath := filepath.Join(demoDir, "target", "demo-app-0.0.1-SNAPSHOT.jar")
	if _, err := os.Stat(jarPath); err != nil {
		t.Fatalf("demo jar missing: %v", err)
	}

	// validation: custom + jvm-opt is rejected with -32602 -> exit 2
	out, code := spm.run("start", "--launcher", "custom", "--name", "demo-custom-bad",
		"--jvm-opt", "-Xmx1G", "--port", "random", "--", "java", "-version")
	if code != 2 {
		t.Fatalf("custom+jvm-opt exited %d, want 2\n%s", code, out)
	}
	if !strings.Contains(out, "custom launcher does not support") {
		t.Errorf("output = %q, want custom limitation message", out)
	}

	spm.mustOK("start", "--launcher", "custom", "--name", "demo-custom",
		"--port", "random", "--timeout", "120s",
		"--", "java", "-jar", jarPath)
	app := spm.status("demo-custom")
	if app.Status != "ready" || app.Spec.Launcher != "custom" {
		t.Fatalf("status = %q launcher = %q, want ready/custom", app.Status, app.Spec.Launcher)
	}
	port := app.ActualPort
	if port < 10000 || port > 60000 {
		t.Fatalf("actualPort = %d, want pool range", port)
	}
	if app.JavaBin != "" {
		t.Errorf("custom javaBin = %q, want empty (unmanaged runtime)", app.JavaBin)
	}
	portAddr := fmt.Sprintf("127.0.0.1:%d", port)
	waitFor(t, 60*time.Second, func() bool {
		resp, err := http.Get(fmt.Sprintf("http://%s/hello", portAddr))
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, "GET /hello returns 200 (custom)")

	spm.mustOK("stop", "demo-custom")
	waitFor(t, 30*time.Second, func() bool {
		conn, err := net.DialTimeout("tcp", portAddr, 300*time.Millisecond)
		if err == nil {
			conn.Close()
			return false
		}
		return true
	}, "port is closed after stop (custom)")
	spm.mustOK("rm", "demo-custom")
	spm.mustOK("kill")
}

// TestDaemonKillAdoption: `spm4a kill` (without --all) must leave apps
// running; the next daemon start re-adopts them per §3.5.
func TestDaemonKillAdoption(t *testing.T) {
	if _, err := exec.LookPath("mvn"); err != nil {
		t.Skip("mvn not found in PATH, skipping e2e")
	}
	spm, demoDir := setup(t, "e2e-adopt")
	home := os.Getenv("SPM4A_HOME")

	t.Cleanup(func() {
		if out, code := spm.run("stop", "demo-adopt", "--now"); code != 0 {
			t.Logf("cleanup stop: exit %d\n%s", code, out)
		}
		if out, code := spm.run("kill", "--all"); code != 0 {
			t.Logf("cleanup kill: exit %d\n%s", code, out)
		}
	})

	spm.mustOK("start", "-f", filepath.Join(demoDir, "spm4a-app.yaml"),
		"--name", "demo-adopt", "--port", "random", "--timeout", "120s")
	before := spm.status("demo-adopt")
	if before.Status != "ready" || before.PID == 0 {
		t.Fatalf("unexpected start state: %+v", before)
	}
	portAddr := fmt.Sprintf("127.0.0.1:%d", before.ActualPort)

	spm.mustOK("kill")

	// kill returns before the daemon fully exits; wait for daemon.json to go.
	daemonJSON := filepath.Join(home, "run", "daemon.json")
	waitFor(t, 10*time.Second, func() bool {
		_, err := os.Stat(daemonJSON)
		return err != nil
	}, "daemon.json removed (daemon exited)")

	// The app must survive the daemon.
	if !pidAlive(before.PID) {
		t.Fatalf("app pid %d died with the daemon", before.PID)
	}
	waitFor(t, 30*time.Second, func() bool {
		resp, err := http.Get(fmt.Sprintf("http://%s/hello", portAddr))
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, "GET /hello still 200 while daemon is down")

	// Any CLI command respawns the daemon, which adopts the running app.
	after := spm.status("demo-adopt")
	if after.Status != "ready" && after.Status != "unready" {
		t.Fatalf("adopted status = %q, want ready or unready", after.Status)
	}
	if after.PID != before.PID {
		t.Errorf("adopted PID = %d, want same process %d", after.PID, before.PID)
	}

	spm.mustOK("stop", "demo-adopt")
	waitFor(t, 30*time.Second, func() bool {
		conn, err := net.DialTimeout("tcp", portAddr, 300*time.Millisecond)
		if err == nil {
			conn.Close()
			return false
		}
		return true
	}, "port is closed after stop (adopted)")
	spm.mustOK("rm", "demo-adopt")
	spm.mustOK("kill")
}

// TestCliErrorCodes audits the §14 exit-code mapping and --json error shape.
func TestCliErrorCodes(t *testing.T) {
	if _, err := exec.LookPath("mvn"); err != nil {
		t.Skip("mvn not found in PATH, skipping e2e")
	}
	spm, demoDir := setup(t, "e2e-err")

	t.Cleanup(func() {
		for _, name := range []string{"demo-timeout"} {
			if out, code := spm.run("stop", name, "--now"); code != 0 {
				t.Logf("cleanup stop %s: exit %d\n%s", name, code, out)
			}
			if out, code := spm.run("rm", name); code != 0 {
				t.Logf("cleanup rm %s: exit %d\n%s", name, code, out)
			}
		}
		if out, code := spm.run("kill", "--all"); code != 0 {
			t.Logf("cleanup kill: exit %d\n%s", code, out)
		}
	})

	// app not found -> 3, and --json produces a machine-readable error
	out, code := spm.run("status", "nosuch", "--json")
	if code != 3 {
		t.Errorf("status nosuch exit = %d, want 3", code)
	}
	if !strings.Contains(out, "-32001") || !strings.Contains(out, `"error"`) {
		t.Errorf("--json error output = %q, want {\"error\":{... -32001 ...}}", out)
	}
	if _, code := spm.run("logs", "nosuch"); code != 3 {
		t.Errorf("logs nosuch exit = %d, want 3", code)
	}
	if _, code := spm.run("rm", "nosuch"); code != 3 {
		t.Errorf("rm nosuch exit = %d, want 3", code)
	}
	if _, code := spm.run("reload", "nosuch"); code != 3 {
		t.Errorf("reload nosuch exit = %d, want 3", code)
	}

	// invalid params -> 2 (daemon-side -32602 for bad xms)
	out, code = spm.run("start", "-f", filepath.Join(demoDir, "spm4a-app.yaml"),
		"--name", "demo-badparams", "--xms", "lots", "--port", "random", "--no-wait")
	if code != 2 {
		t.Errorf("bad xms exit = %d, want 2\n%s", code, out)
	}

	// usage error -> 2 (unknown flag)
	if _, code := spm.run("ls", "--wat"); code != 2 {
		t.Errorf("unknown flag exit = %d, want 2", code)
	}

	// ready timeout -> 6: health path never answers UP
	out, code = spm.run("start", "-f", filepath.Join(demoDir, "spm4a-app.yaml"),
		"--name", "demo-timeout", "--port", "random",
		"--health-path", "/actuator/nope", "--timeout", "3s")
	if code != 6 {
		t.Errorf("ready timeout exit = %d, want 6\n%s", code, out)
	}
	spm.mustOK("rm", "demo-timeout")

	// daemon unavailable -> 5: SPM4A_HOME pointing at a file, spawn must fail
	bin := os.Getenv("SPM4A_TEST_BIN")
	if bin == "" {
		t.Skip("SPM4A_TEST_BIN not set")
	}
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := []string{}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "SPM4A_HOME=") {
			env = append(env, kv)
		}
	}
	env = append(env, "SPM4A_HOME="+blocker)
	cmd := exec.Command(bin, "ls")
	cmd.Env = env
	outB, err := cmd.CombinedOutput()
	code = 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("run isolated spm4a: %v", err)
	}
	if code != 5 {
		t.Errorf("daemon-unavailable exit = %d, want 5\n%s", code, outB)
	}
}
