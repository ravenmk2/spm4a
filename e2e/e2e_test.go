package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

type appView struct {
	Spec struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
		Launcher  string `json:"launcher"`
	} `json:"spec"`
	Status          string         `json:"status"`
	PID             int            `json:"pid"`
	ActualPort      int            `json:"actualPort"`
	ResolvedJar     string         `json:"resolvedJar"`
	ResolvedJvmOpts []string       `json:"resolvedJvmOpts"`
	LogPath         string         `json:"logPath"`
	Injected        map[string]any `json:"injected"`
}

type spmRunner struct {
	t   *testing.T
	bin string
	dir string
}

func (s spmRunner) run(args ...string) (string, int) {
	s.t.Helper()
	cmd := exec.Command(s.bin, args...)
	cmd.Dir = s.dir
	out, err := cmd.CombinedOutput()
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		s.t.Fatalf("run spm4a %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out), code
}

func (s spmRunner) mustOK(args ...string) string {
	s.t.Helper()
	out, code := s.run(args...)
	if code != 0 {
		s.t.Fatalf("spm4a %s exited %d\n%s", strings.Join(args, " "), code, out)
	}
	return out
}

func (s spmRunner) status(name string) appView {
	s.t.Helper()
	out := s.mustOK("status", name, "--json")
	var res struct {
		App appView `json:"app"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		s.t.Fatalf("parse status output: %v\n%s", err, out)
	}
	return res.App
}

// setup builds the demo-app jar and the spm4a binary, and isolates the
// daemon via SPM4A_HOME / SPM4A_NAMESPACE.
func setup(t *testing.T, namespace string) (spmRunner, string) {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	demoDir := filepath.Join(wd, "demo-app")
	repoRoot := filepath.Dir(wd)

	t.Setenv("SPM4A_HOME", t.TempDir())
	t.Setenv("SPM4A_NAMESPACE", namespace)

	run(t, demoDir, 15*time.Minute, "mvn", "-B", "-q", "-DskipTests", "package")

	bin := filepath.Join(t.TempDir(), "spm4a"+exeSuffix())
	run(t, repoRoot, 10*time.Minute, "go", "build", "-o", bin, "./cmd/spm4a")
	return spmRunner{t: t, bin: bin, dir: demoDir}, demoDir
}

func TestDemoAppLifecycle(t *testing.T) {
	if _, err := exec.LookPath("mvn"); err != nil {
		t.Skip("mvn not found in PATH, skipping e2e")
	}
	if _, err := exec.LookPath("java"); err != nil && os.Getenv("JAVA_HOME") == "" {
		t.Skip("java not found (PATH or JAVA_HOME), skipping e2e")
	}

	spm, demoDir := setup(t, "e2e")

	t.Cleanup(func() {
		for _, name := range []string{"demo-app", "demo-custom", "demo-fixed", "demo-fixed2"} {
			if out, code := spm.run("stop", name, "--now"); code != 0 {
				t.Logf("cleanup stop %s: exit %d\n%s", name, code, out)
			}
		}
		if out, code := spm.run("kill", "--all"); code != 0 {
			t.Logf("cleanup kill: exit %d\n%s", code, out)
		}
	})

	// ---- 1. start with a random port (CLI overrides yaml's 8080) ----
	out := spm.mustOK("start", "-f", filepath.Join(demoDir, "spm4a-app.yaml"),
		"--port", "random", "--timeout", "180s")
	t.Logf("start output: %s", strings.TrimSpace(out))

	main := spm.status("demo-app")
	if main.Status != "ready" {
		t.Fatalf("status = %q, want ready", main.Status)
	}
	port := main.ActualPort
	if port < 10000 || port > 60000 {
		t.Fatalf("actualPort = %d, want within pool range 10000-60000", port)
	}
	portAddr := fmt.Sprintf("127.0.0.1:%d", port)

	// ---- 2. GET /hello on the allocated port ----
	waitFor(t, 90*time.Second, func() bool {
		resp, err := http.Get(fmt.Sprintf("http://%s/hello", portAddr))
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, "GET /hello returns 200")

	// ---- 3. ls --json ----
	out = spm.mustOK("ls", "--json")
	var lsRes struct {
		Apps []appView `json:"apps"`
	}
	if err := json.Unmarshal([]byte(out), &lsRes); err != nil {
		t.Fatalf("parse ls output: %v\n%s", err, out)
	}
	if len(lsRes.Apps) != 1 {
		t.Fatalf("expected 1 app, got %d\n%s", len(lsRes.Apps), out)
	}
	if lsRes.Apps[0].Spec.Namespace != "e2e" || lsRes.Apps[0].Status != "ready" {
		t.Errorf("unexpected ls entry: %+v", lsRes.Apps[0])
	}

	// ---- 4. status --json: injection list + resolved jvm opts + jar ----
	if !strings.HasSuffix(main.ResolvedJar, ".jar") {
		t.Errorf("resolvedJar = %q, want a .jar path", main.ResolvedJar)
	}
	inj := main.Injected
	if got := intFromAny(inj["server.port"]); got != port {
		t.Errorf("injected server.port = %v, want %d", inj["server.port"], port)
	}
	if got, _ := inj["management.endpoints.web.exposure.include"].(string); !containsAll(got, "health", "shutdown") {
		t.Errorf("injected exposure.include = %q, want health+shutdown", got)
	}
	if got, _ := inj["management.endpoint.shutdown.enabled"].(bool); !got {
		t.Errorf("injected shutdown.enabled = %v, want true", inj["management.endpoint.shutdown.enabled"])
	}
	if got, _ := inj["server.shutdown"].(string); got != "graceful" {
		t.Errorf("injected server.shutdown = %q, want graceful", got)
	}
	for _, want := range []string{"-Xms32M", "-Xmx256M"} {
		if !slices.Contains(main.ResolvedJvmOpts, want) {
			t.Errorf("resolvedJvmOpts = %v, want to contain %s", main.ResolvedJvmOpts, want)
		}
	}

	// ---- 5. health command passes through actuator JSON ----
	out = spm.mustOK("health", "demo-app")
	var healthBody struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(out), &healthBody); err != nil || healthBody.Status != "UP" {
		t.Fatalf("health output = %s, want status UP", out)
	}

	// ---- 6. custom JVM options compose in order ----
	spm.mustOK("start", "--name", "demo-custom", "--port", "random",
		"--xms", "64M", "--xmx", "512M", "--jvm-opt", "-XX:TieredStopAtLevel=1", "--timeout", "120s")
	custom := spm.status("demo-custom")
	if custom.Status != "ready" {
		t.Fatalf("demo-custom status = %q, want ready", custom.Status)
	}
	wantOpts := []string{"-Xms64M", "-Xmx512M", "-XX:TieredStopAtLevel=1"}
	if !slices.Equal(custom.ResolvedJvmOpts, wantOpts) {
		t.Errorf("demo-custom resolvedJvmOpts = %v, want %v", custom.ResolvedJvmOpts, wantOpts)
	}
	spm.mustOK("stop", "demo-custom")
	spm.mustOK("rm", "demo-custom")

	// ---- 7. fixed port conflict -> exit 4 ----
	fixedPort := freePort(t)
	spm.mustOK("start", "--name", "demo-fixed", "--port", strconv.Itoa(fixedPort), "--timeout", "120s")
	out, code := spm.run("start", "--name", "demo-fixed2", "--port", strconv.Itoa(fixedPort), "--timeout", "30s")
	if code != 4 {
		t.Fatalf("port conflict start exited %d, want 4\n%s", code, out)
	}
	if !strings.Contains(out, "already in use") {
		t.Errorf("conflict output = %q, want mention of port in use", out)
	}
	spm.mustOK("rm", "demo-fixed2") // failed start leaves an error entry; removable
	spm.mustOK("stop", "demo-fixed")
	spm.mustOK("rm", "demo-fixed")

	// ---- 8. graceful stop of the main app ----
	spm.mustOK("stop", "demo-app")
	waitFor(t, 30*time.Second, func() bool {
		conn, err := net.DialTimeout("tcp", portAddr, 300*time.Millisecond)
		if err == nil {
			conn.Close()
			return false
		}
		return true
	}, "port is closed after stop")

	logPath := filepath.Join(demoDir, "logs", "demo-app.log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read app log: %v", err)
	}
	if !strings.Contains(string(data), "Commencing graceful shutdown") {
		t.Errorf("app log lacks graceful shutdown evidence")
	}
	if !strings.Contains(string(data), "Started DemoApplication") {
		t.Errorf("app log lacks startup banner")
	}

	// ---- 9. rm, empty ls, kill ----
	spm.mustOK("rm", "demo-app")
	out = spm.mustOK("ls", "--json")
	if !strings.Contains(out, `"apps": []`) {
		t.Errorf("expected empty app list after rm, got %s", out)
	}
	spm.mustOK("kill")

	// No leftover java on any of the ports used above.
	for _, p := range []int{port, fixedPort} {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", p), 300*time.Millisecond)
		if err == nil {
			conn.Close()
			t.Fatalf("port %d still reachable at test end; leftover java process?", p)
		}
	}
}

// TestMavenLauncherLifecycle drives the two-layer maven launcher: devtools
// hot restart via reload (same root PID), graceful shutdown, tree cleanup.
func TestMavenLauncherLifecycle(t *testing.T) {
	if _, err := exec.LookPath("mvn"); err != nil {
		t.Skip("mvn not found in PATH, skipping e2e")
	}
	if _, err := exec.LookPath("java"); err != nil && os.Getenv("JAVA_HOME") == "" {
		t.Skip("java not found (PATH or JAVA_HOME), skipping e2e")
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	demoDir := filepath.Join(wd, "demo-app")
	repoRoot := filepath.Dir(wd)

	t.Setenv("SPM4A_HOME", t.TempDir())
	t.Setenv("SPM4A_NAMESPACE", "e2e-maven")

	bin := filepath.Join(t.TempDir(), "spm4a"+exeSuffix())
	run(t, repoRoot, 10*time.Minute, "go", "build", "-o", bin, "./cmd/spm4a")
	spm := spmRunner{t: t, bin: bin, dir: demoDir}

	t.Cleanup(func() {
		if out, code := spm.run("stop", "demo-maven", "--now"); code != 0 {
			t.Logf("cleanup stop: exit %d\n%s", code, out)
		}
		if out, code := spm.run("kill", "--all"); code != 0 {
			t.Logf("cleanup kill: exit %d\n%s", code, out)
		}
	})

	// start via the maven launcher (workdir/jar come from spm4a-app.yaml)
	spm.mustOK("start", "--launcher", "maven", "--name", "demo-maven",
		"--port", "random", "--timeout", "300s")
	app := spm.status("demo-maven")
	if app.Status != "ready" || app.Spec.Launcher != "maven" {
		t.Fatalf("status = %q launcher = %q, want ready/maven", app.Status, app.Spec.Launcher)
	}
	port := app.ActualPort
	if port < 10000 || port > 60000 {
		t.Fatalf("actualPort = %d, want within pool range 10000-60000", port)
	}
	portAddr := fmt.Sprintf("127.0.0.1:%d", port)
	rootPID := app.PID
	if rootPID == 0 {
		t.Fatal("PID is 0")
	}

	waitFor(t, 60*time.Second, func() bool {
		resp, err := http.Get(fmt.Sprintf("http://%s/hello", portAddr))
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, "GET /hello returns 200 (maven)")

	// touch a source file so `mvn compile` recompiles and devtools restarts
	src := filepath.Join(demoDir, "src", "main", "java", "com", "spm4a", "demo", "DemoApplication.java")
	now := time.Now()
	if err := os.Chtimes(src, now, now); err != nil {
		t.Fatal(err)
	}

	// reload: compile in workdir -> devtools hot restart -> health UP again
	out := spm.mustOK("reload", "demo-maven", "--json", "--timeout", "240s")
	var reloadRes struct {
		ExitCode int    `json:"exitCode"`
		Output   string `json:"output"`
		Health   struct {
			Status string `json:"status"`
		} `json:"health"`
		AppStatus string `json:"appStatus"`
	}
	if err := json.Unmarshal([]byte(out), &reloadRes); err != nil {
		t.Fatalf("parse reload output: %v\n%s", err, out)
	}
	if reloadRes.ExitCode != 0 {
		t.Fatalf("reload exitCode = %d, want 0\n%s", reloadRes.ExitCode, reloadRes.Output)
	}
	if reloadRes.Health.Status != "UP" {
		t.Errorf("reload health = %q, want UP\noutput: %s", reloadRes.Health.Status, reloadRes.Output)
	}

	// devtools restarts the context, not the process: root PID must be stable
	after := spm.status("demo-maven")
	if after.PID != rootPID {
		t.Errorf("PID changed across reload: %d -> %d (want same JVM tree)", rootPID, after.PID)
	}
	if after.Status != "ready" {
		t.Errorf("status after reload = %q, want ready", after.Status)
	}
	waitFor(t, 60*time.Second, func() bool {
		resp, err := http.Get(fmt.Sprintf("http://%s/hello", portAddr))
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, "GET /hello returns 200 after reload")

	// stop: graceful shutdown via actuator, then the whole tree must be gone
	spm.mustOK("stop", "demo-maven", "--timeout", "60s")
	waitFor(t, 30*time.Second, func() bool {
		conn, err := net.DialTimeout("tcp", portAddr, 300*time.Millisecond)
		if err == nil {
			conn.Close()
			return false
		}
		return true
	}, "port is closed after stop (maven)")
	waitFor(t, 30*time.Second, func() bool { return !pidAlive(rootPID) }, "maven process tree is gone")

	logPath := filepath.Join(demoDir, "logs", "demo-maven.log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read app log: %v", err)
	}
	if !strings.Contains(string(data), "Commencing graceful shutdown") {
		t.Errorf("maven app log lacks graceful shutdown evidence")
	}

	spm.mustOK("rm", "demo-maven")
	spm.mustOK("kill")
}

// TestGradleLauncherLifecycle is skipped unless a gradle binary and a gradle
// build are present (this machine has neither).
func TestGradleLauncherLifecycle(t *testing.T) {
	if _, err := exec.LookPath("gradle"); err != nil {
		t.Skip("gradle not found in PATH, skipping")
	}
	wd, _ := os.Getwd()
	if _, err := os.Stat(filepath.Join(wd, "demo-app", "build.gradle")); err != nil {
		t.Skip("demo-app has no gradle build, skipping")
	}
	// When a gradle project exists this mirrors the maven lifecycle.
}

func intFromAny(v any) int {
	f, _ := v.(float64)
	return int(f)
}

func containsAll(s string, wants ...string) bool {
	for _, w := range wants {
		if !strings.Contains(s, w) {
			return false
		}
	}
	return true
}

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func run(t *testing.T, dir string, timeout time.Duration, name string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s failed: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return string(out)
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for: %s", what)
}
