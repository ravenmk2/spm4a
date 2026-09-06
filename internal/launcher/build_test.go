package launcher

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestBuildJvmOptsOrder(t *testing.T) {
	got := BuildJvmOpts("-agentlib:jdwp=..address=*:5005", "32M", "256M", []string{"-XX:+UseG1GC"})
	want := []string{"-agentlib:jdwp=..address=*:5005", "-Xms32M", "-Xmx256M", "-XX:+UseG1GC"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v (jdwp + xms/xmx + jvm-opts)", got, want)
	}
	// disabled heap entries skipped
	got = BuildJvmOpts("", "", "", nil)
	if len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
}

func TestBuildMavenCommand(t *testing.T) {
	cmd := BuildMavenCommand("mvn", []string{"-Xms32M", "-Xmx256M"}, []string{"--a=1", "--b=2"})
	want := []string{
		"mvn", "spring-boot:run",
		"-Dspring-boot.run.jvmArguments=-Xms32M -Xmx256M",
		"-Dspring-boot.run.arguments=--a=1 --b=2",
	}
	if !slices.Equal(cmd.Args, want) {
		t.Errorf("args = %v, want %v", cmd.Args, want)
	}
}

func TestBuildMavenCommandNoOpts(t *testing.T) {
	cmd := BuildMavenCommand("mvn", nil, nil)
	want := []string{"mvn", "spring-boot:run"}
	if !slices.Equal(cmd.Args, want) {
		t.Errorf("args = %v, want %v", cmd.Args, want)
	}
}

func TestGradleInitScript(t *testing.T) {
	s := GradleInitScript([]string{"-Xms32M", "-Xmx256M", "-XX:TieredStopAtLevel=1"})
	for _, want := range []string{
		"bootRun",
		"task.jvmArgs = ['-Xms32M', '-Xmx256M', '-XX:TieredStopAtLevel=1']",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("init script missing %q:\n%s", want, s)
		}
	}
}

func TestGradleInitScriptEscaping(t *testing.T) {
	s := GradleInitScript([]string{`-Ddir=C:\tmp\it's`})
	if !strings.Contains(s, `'-Ddir=C:\\tmp\\it\'s'`) {
		t.Errorf("init script not escaped properly:\n%s", s)
	}
}

func TestGradleInitScriptEmpty(t *testing.T) {
	s := GradleInitScript(nil)
	if !strings.Contains(s, "task.jvmArgs = []") {
		t.Errorf("init script should set an empty list:\n%s", s)
	}
}

func TestBuildGradleCommand(t *testing.T) {
	cmd := BuildGradleCommand("gradle", []string{"--a=1"}, "C:\\run\\spm4a-init-x.gradle")
	want := []string{"gradle", "bootRun", "-I", `C:\run\spm4a-init-x.gradle`, "--args=--a=1"}
	if !slices.Equal(cmd.Args, want) {
		t.Errorf("args = %v, want %v", cmd.Args, want)
	}
}

func TestBuildGradleCommandNoInit(t *testing.T) {
	cmd := BuildGradleCommand("gradle", nil, "")
	want := []string{"gradle", "bootRun"}
	if !slices.Equal(cmd.Args, want) {
		t.Errorf("args = %v, want %v", cmd.Args, want)
	}
}

func TestResolveGradlePrefersWrapper(t *testing.T) {
	dir := t.TempDir()
	writeExecutable(t, dir, "gradlew")
	writeExecutable(t, dir, "gradlew.bat")
	got, err := ResolveGradle(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(strings.ToLower(got), "gradlew") && !strings.HasSuffix(strings.ToLower(got), "gradlew.bat") {
		t.Errorf("ResolveGradle = %q, want the wrapper", got)
	}
}

func TestResolveGradleNotFound(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := ResolveGradle(t.TempDir()); err == nil || !strings.Contains(err.Error(), "gradle not found") {
		t.Errorf("err = %v, want gradle not found", err)
	}
}

func writeExecutable(t *testing.T, dir, name string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("@echo off\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}
