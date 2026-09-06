package launcher

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// ResolveJar applies the three jar resolution rules from the design:
// absolute path used as-is; relative path resolved against workdir;
// glob patterns must match exactly one file.
func ResolveJar(workdir, jar string) (string, error) {
	jar = filepath.FromSlash(jar)
	if jar == "" {
		return "", fmt.Errorf("jar is required for launcher=jar")
	}
	if filepath.IsAbs(jar) {
		if _, err := os.Stat(jar); err != nil {
			return "", fmt.Errorf("jar not found: %s", jar)
		}
		return filepath.Clean(jar), nil
	}
	if strings.ContainsAny(jar, "*?[") {
		matches, err := filepath.Glob(filepath.Join(workdir, jar))
		if err != nil {
			return "", fmt.Errorf("invalid jar glob %q: %v", jar, err)
		}
		switch len(matches) {
		case 0:
			return "", fmt.Errorf("jar not found: %q matches nothing under workdir %s; build the app first", jar, workdir)
		case 1:
			abs, err := filepath.Abs(matches[0])
			if err != nil {
				return "", err
			}
			return abs, nil
		default:
			return "", fmt.Errorf("jar glob %q is ambiguous, matches:\n  %s", jar, strings.Join(matches, "\n  "))
		}
	}
	p := filepath.Join(workdir, jar)
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("jar not found: %s (resolved against workdir %s)", jar, workdir)
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	return abs, nil
}

// ResolveJava: explicit absolute JDK home, else JAVA_HOME, else PATH.
// The JDK registry (names / major versions) is M4.
func ResolveJava(jdk string) (string, error) {
	if jdk != "" {
		if !filepath.IsAbs(jdk) {
			return "", fmt.Errorf("jdk %q: only absolute JDK paths are supported in M1", jdk)
		}
		p := filepath.Join(jdk, "bin", javaExe())
		if _, err := os.Stat(p); err != nil {
			return "", fmt.Errorf("java not found under JDK home %s", jdk)
		}
		return p, nil
	}
	if home := os.Getenv("JAVA_HOME"); home != "" {
		p := filepath.Join(home, "bin", javaExe())
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	if p, err := exec.LookPath("java"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("java not found: set JAVA_HOME or add java to PATH")
}

func javaExe() string {
	if runtime.GOOS == "windows" {
		return "java.exe"
	}
	return "java"
}

// JavaExeName returns the platform java binary name ("java.exe" on Windows).
func JavaExeName() string { return javaExe() }

func BuildJarCommand(javaBin, jarPath string, jvmOpts, args []string) *exec.Cmd {
	argv := append([]string{}, jvmOpts...)
	argv = append(argv, "-jar", jarPath)
	argv = append(argv, args...)
	return exec.Command(javaBin, argv...)
}

var heapSizeRe = regexp.MustCompile(`^\d+[kKmMgG]$`)

func ValidHeapSize(s string) bool { return heapSizeRe.MatchString(s) }

// BuildJvmOpts composes §8.4: [xms/xmx] + [jvm-opts] (jdwp lands in M4).
// An empty xms/xmx disables that entry.
func BuildJvmOpts(xms, xmx string, user []string) []string {
	var out []string
	if xms != "" {
		out = append(out, "-Xms"+xms)
	}
	if xmx != "" {
		out = append(out, "-Xmx"+xmx)
	}
	return append(out, user...)
}

var logVarRe = regexp.MustCompile(`\$\{([A-Za-z]+)\}`)

var knownLogVars = map[string]bool{
	"name": true, "namespace": true, "workdir": true, "pid": true, "ts": true,
}

func ValidateLogExpr(expr string) error {
	for _, m := range logVarRe.FindAllStringSubmatch(expr, -1) {
		if !knownLogVars[m[1]] {
			return fmt.Errorf("log-file: unknown variable ${%s} (known: ${name} ${namespace} ${workdir} ${pid} ${ts})", m[1])
		}
	}
	return nil
}

func LogExprHasVar(expr, name string) bool {
	for _, m := range logVarRe.FindAllStringSubmatch(expr, -1) {
		if m[1] == name {
			return true
		}
	}
	return false
}

func ExpandLogExpr(expr string, vars map[string]string) string {
	return logVarRe.ReplaceAllStringFunc(expr, func(m string) string {
		name := m[2 : len(m)-1]
		if v, ok := vars[name]; ok {
			return v
		}
		return m
	})
}

// ResolveLogPath expands expr and resolves it against workdir if relative.
func ResolveLogPath(expr, workdir string, vars map[string]string) string {
	p := ExpandLogExpr(expr, vars)
	if !filepath.IsAbs(p) {
		p = filepath.Join(workdir, p)
	}
	return filepath.Clean(p)
}
