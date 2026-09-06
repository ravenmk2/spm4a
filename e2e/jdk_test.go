package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// currentJDKHome locates the JDK running the tests (JAVA_HOME, else PATH java).
func currentJDKHome(t *testing.T) string {
	t.Helper()
	if h := os.Getenv("JAVA_HOME"); h != "" {
		return h
	}
	p, err := exec.LookPath("java")
	if err != nil {
		t.Skip("cannot locate a JDK home (no JAVA_HOME, no java on PATH)")
	}
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		p = resolved
	}
	return filepath.Dir(filepath.Dir(p))
}

func TestJdkRegistryCommands(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repoRoot := filepath.Dir(wd)

	t.Setenv("SPM4A_HOME", t.TempDir())
	t.Setenv("SPM4A_NAMESPACE", "e2e-jdk")

	bin := filepath.Join(t.TempDir(), "spm4a"+exeSuffix())
	run(t, repoRoot, 10*time.Minute, "go", "build", "-o", bin, "./cmd/spm4a")
	spm := spmRunner{t: t, bin: bin, dir: wd}

	jdkHome := currentJDKHome(t)

	// empty registry
	out := spm.mustOK("jdk", "ls", "--json")
	var lsRes struct {
		JDKs []struct {
			Name    string `json:"name"`
			Version string `json:"version"`
			Major   int    `json:"major"`
			Home    string `json:"home"`
			Default bool   `json:"default"`
		} `json:"jdks"`
	}
	if err := json.Unmarshal([]byte(out), &lsRes); err != nil {
		t.Fatalf("parse jdk ls: %v\n%s", err, out)
	}
	if len(lsRes.JDKs) != 0 {
		t.Fatalf("fresh registry not empty: %s", out)
	}

	// add the current JDK with an explicit name
	out = spm.mustOK("jdk", "add", jdkHome, "--name", "e2ejdk")
	t.Logf("jdk add: %s", strings.TrimSpace(out))

	out = spm.mustOK("jdk", "ls", "--json")
	if err := json.Unmarshal([]byte(out), &lsRes); err != nil {
		t.Fatalf("parse jdk ls: %v\n%s", err, out)
	}
	var added *struct {
		Name    string `json:"name"`
		Version string `json:"version"`
		Major   int    `json:"major"`
		Home    string `json:"home"`
		Default bool   `json:"default"`
	}
	for i := range lsRes.JDKs {
		if lsRes.JDKs[i].Name == "e2ejdk" {
			added = &lsRes.JDKs[i]
		}
	}
	if added == nil {
		t.Fatalf("added jdk not in ls: %s", out)
	}
	if !samePathFold(added.Home, jdkHome) {
		t.Errorf("home = %q, want %q", added.Home, jdkHome)
	}
	if added.Major <= 0 {
		t.Errorf("major = %d, want > 0", added.Major)
	}

	// scan must find at least JAVA_HOME / PATH java
	spm.mustOK("jdk", "scan")
	out = spm.mustOK("jdk", "ls", "--json")
	if err := json.Unmarshal([]byte(out), &lsRes); err != nil {
		t.Fatalf("parse jdk ls: %v\n%s", err, out)
	}
	found := false
	for _, e := range lsRes.JDKs {
		if samePathFold(e.Home, jdkHome) {
			found = true
		}
	}
	if !found {
		t.Errorf("scan did not register the current JDK %s: %s", jdkHome, out)
	}
}

func TestJdkReferenceAndDebug(t *testing.T) {
	if _, err := exec.LookPath("mvn"); err != nil {
		t.Skip("mvn not found in PATH, skipping e2e")
	}
	spm, demoDir := setup(t, "e2e-m4")
	jdkHome := currentJDKHome(t)

	t.Cleanup(func() {
		for _, name := range []string{"demo-jdkname", "demo-jdkmajor", "demo-dbg", "demo-dbgfixed"} {
			if out, code := spm.run("stop", name, "--now"); code != 0 {
				t.Logf("cleanup stop %s: exit %d\n%s", name, code, out)
			}
		}
		if out, code := spm.run("kill", "--all"); code != 0 {
			t.Logf("cleanup kill: exit %d\n%s", code, out)
		}
	})

	// register the current JDK, then reference it by name and by major
	spm.mustOK("jdk", "add", jdkHome, "--name", "e2ejdk")
	wantJavaBin := filepath.Join(jdkHome, "bin", javaExeName())

	majorOut := spm.mustOK("jdk", "ls", "--json")
	var lsRes struct {
		JDKs []struct {
			Name  string `json:"name"`
			Major int    `json:"major"`
		} `json:"jdks"`
	}
	if err := json.Unmarshal([]byte(majorOut), &lsRes); err != nil {
		t.Fatal(err)
	}
	major := ""
	for _, e := range lsRes.JDKs {
		if e.Name == "e2ejdk" {
			major = strconv.Itoa(e.Major)
		}
	}
	if major == "" {
		t.Fatal("e2ejdk not registered")
	}

	for i, ref := range []string{"e2ejdk", major} {
		name := "demo-jdkname"
		if i == 1 {
			name = "demo-jdkmajor"
		}
		spm.mustOK("start", "-f", filepath.Join(demoDir, "spm4a-app.yaml"),
			"--name", name, "--jdk", ref, "--port", "random", "--timeout", "120s")
		app := spm.status(name)
		if app.Status != "ready" {
			t.Fatalf("%s: status = %q", name, app.Status)
		}
		if !samePathFold(app.JavaBin, wantJavaBin) {
			t.Errorf("%s: javaBin = %q, want %q", name, app.JavaBin, wantJavaBin)
		}
		spm.mustOK("stop", name)
		// ephemeral default: stop already removed the record
		if out, code := spm.run("rm", name); code != 3 {
			t.Fatalf("rm after stop (ephemeral) exited %d, want 3\n%s", code, out)
		}
	}

	// debug with a random port: JDWP handshake against the allocated port
	spm.mustOK("start", "-f", filepath.Join(demoDir, "spm4a-app.yaml"),
		"--name", "demo-dbg", "--debug", "--port", "random", "--timeout", "120s")
	dbg := spm.status("demo-dbg")
	if dbg.DebugPort < 10000 || dbg.DebugPort > 60000 {
		t.Fatalf("debugPort = %d, want pool range 10000-60000", dbg.DebugPort)
	}
	if len(dbg.ResolvedJvmOpts) == 0 || !strings.Contains(dbg.ResolvedJvmOpts[0], "jdwp") {
		t.Errorf("resolvedJvmOpts = %v, want jdwp agent first", dbg.ResolvedJvmOpts)
	}
	if !strings.Contains(dbg.ResolvedJvmOpts[0], "address=*:") {
		t.Errorf("jdwp opt = %q, want address=*: (JDK>=9 syntax)", dbg.ResolvedJvmOpts[0])
	}
	jdwpHandshake(t, dbg.DebugPort)
	spm.mustOK("stop", "demo-dbg")

	// debug with a fixed port
	fixedDebug := freePort(t)
	spm.mustOK("start", "-f", filepath.Join(demoDir, "spm4a-app.yaml"),
		"--name", "demo-dbgfixed", fmt.Sprintf("--debug=%d", fixedDebug), "--port", "random", "--timeout", "120s")
	dbgf := spm.status("demo-dbgfixed")
	if dbgf.DebugPort != fixedDebug {
		t.Errorf("debugPort = %d, want %d", dbgf.DebugPort, fixedDebug)
	}
	jdwpHandshake(t, fixedDebug)
	spm.mustOK("stop", "demo-dbgfixed")

	spm.mustOK("kill")
}

// jdwpHandshake performs the real JDWP handshake: the debugger sends
// "JDWP-Handshake" and the JVM replies with the same 14 bytes.
func jdwpHandshake(t *testing.T, port int) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 3*time.Second)
	if err != nil {
		t.Fatalf("dial debug port %d: %v", port, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte("JDWP-Handshake")); err != nil {
		t.Fatalf("write handshake: %v", err)
	}
	buf := make([]byte, 14)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("read handshake reply: %v", err)
	}
	if string(buf) != "JDWP-Handshake" {
		t.Fatalf("handshake reply = %q, want JDWP-Handshake", buf)
	}
}

func samePathFold(a, b string) bool {
	if ra, err := filepath.EvalSymlinks(a); err == nil {
		a = ra
	}
	if rb, err := filepath.EvalSymlinks(b); err == nil {
		b = rb
	}
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

func javaExeName() string {
	if runtime.GOOS == "windows" {
		return "java.exe"
	}
	return "java"
}
