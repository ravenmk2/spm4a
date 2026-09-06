package e2e

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// idleRunner builds the binary and isolates a daemon; no java/maven needed.
func idleRunner(t *testing.T, idleExit string) spmRunner {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repoRoot := filepath.Dir(wd)
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.yaml"),
		[]byte("idle-exit: "+idleExit+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SPM4A_HOME", home)
	t.Setenv("SPM4A_NAMESPACE", "e2e-idle")
	bin := filepath.Join(t.TempDir(), "spm4a"+exeSuffix())
	run(t, repoRoot, 10*time.Minute, "go", "build", "-o", bin, "./cmd/spm4a")
	return spmRunner{t: t, bin: bin, dir: wd}
}

func daemonJSONExists() func() bool {
	home := os.Getenv("SPM4A_HOME")
	return func() bool {
		_, err := os.Stat(filepath.Join(home, "run", "daemon.json"))
		return err == nil
	}
}

func TestIdleExitDelayed(t *testing.T) {
	spm := idleRunner(t, "2s")
	spm.mustOK("ls") // spawns the daemon; no apps -> idle
	exists := daemonJSONExists()
	if !exists() {
		t.Fatal("daemon.json missing right after ls (daemon should still be up)")
	}
	// still alive shortly after — immediate mode would have exited in ~200ms
	time.Sleep(1200 * time.Millisecond)
	if !exists() {
		t.Fatal("daemon exited before the 2s idle delay elapsed")
	}
	// and it does exit once the delay elapses
	waitFor(t, 10*time.Second, func() bool { return !exists() }, "daemon exits after idle delay")
}

func TestIdleExitNever(t *testing.T) {
	spm := idleRunner(t, "never")
	spm.mustOK("ls")
	exists := daemonJSONExists()
	if !exists() {
		t.Fatal("daemon.json missing right after ls")
	}
	// well past the immediate-exit window; daemon must stay up
	time.Sleep(1500 * time.Millisecond)
	if !exists() {
		t.Fatal("daemon exited despite idle-exit: never")
	}
	spm.mustOK("kill") // explicit shutdown still works
	waitFor(t, 10*time.Second, func() bool { return !exists() }, "daemon exits after kill")
}
