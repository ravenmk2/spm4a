package jdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Entry struct {
	Name    string `json:"name"`
	Home    string `json:"home"`
	Version string `json:"version"`
	Major   int    `json:"major"`
}

// Registry is the CLI-maintained JDK registry at <SPM4A_HOME>/jdks.json.
type Registry struct {
	path    string
	Entries []Entry `json:"jdks"`
}

func Path(home string) string { return filepath.Join(home, "jdks.json") }

func Load(home string) (*Registry, error) {
	r := &Registry{path: Path(home)}
	data, err := os.ReadFile(r.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return r, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, r); err != nil {
		return nil, fmt.Errorf("parse %s: %w", r.path, err)
	}
	return r, nil
}

func (r *Registry) Save() error {
	if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		return err
	}
	sort.Slice(r.Entries, func(i, j int) bool {
		if r.Entries[i].Major != r.Entries[j].Major {
			return r.Entries[i].Major < r.Entries[j].Major
		}
		return r.Entries[i].Name < r.Entries[j].Name
	})
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	tmp := r.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, r.path)
}

func javaBinFor(home string) string {
	exe := "java"
	if runtime.GOOS == "windows" {
		exe = "java.exe"
	}
	return filepath.Join(home, "bin", exe)
}

var versionRe = regexp.MustCompile(`(?m)(?:openjdk|java) version "([^"]+)"`)

// ParseVersion extracts the version string and major version from
// `java -version` output ("1.8.0_392" -> 8, "17.0.9" -> 17).
func ParseVersion(out string) (string, int, error) {
	m := versionRe.FindStringSubmatch(out)
	if m == nil {
		return "", 0, fmt.Errorf("no version found in java -version output")
	}
	version := m[1]
	parts := strings.Split(version, ".")
	comp := parts[0]
	if comp == "1" && len(parts) > 1 {
		comp = parts[1]
	}
	digits := comp
	for i, c := range comp {
		if c < '0' || c > '9' {
			digits = comp[:i]
			break
		}
	}
	major, err := strconv.Atoi(digits)
	if err != nil {
		return "", 0, fmt.Errorf("cannot parse major version from %q", version)
	}
	return version, major, nil
}

// Probe runs <home>/bin/java -version once (output is on stderr).
func Probe(home string) (version string, major int, err error) {
	bin := javaBinFor(home)
	if _, err := os.Stat(bin); err != nil {
		return "", 0, fmt.Errorf("no java binary at %s", bin)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "-version").CombinedOutput()
	if err != nil {
		return "", 0, fmt.Errorf("java -version: %w", err)
	}
	return ParseVersion(string(out))
}

func normKey(home string) string {
	abs, err := filepath.Abs(home)
	if err == nil {
		home = abs
	}
	if resolved, err := filepath.EvalSymlinks(home); err == nil {
		home = resolved
	}
	home = filepath.Clean(home)
	if runtime.GOOS == "windows" {
		home = strings.ToLower(home)
	}
	return home
}

func (r *Registry) byHome(home string) *Entry {
	key := normKey(home)
	for i := range r.Entries {
		if normKey(r.Entries[i].Home) == key {
			return &r.Entries[i]
		}
	}
	return nil
}

// Add registers (or updates) the JDK at home; an explicit name overrides any
// existing entry with that name.
func (r *Registry) Add(home, name string) (*Entry, error) {
	st, err := os.Stat(home)
	if err != nil || !st.IsDir() {
		return nil, fmt.Errorf("JDK home not found: %s", home)
	}
	abs, _ := filepath.Abs(home)
	home = filepath.Clean(abs)
	version, major, err := Probe(home)
	if err != nil {
		return nil, err
	}
	if e := r.byHome(home); e != nil {
		e.Version, e.Major = version, major
		if name != "" {
			r.removeName(name)
			e.Name = name
		}
		_ = r.Save()
		return e, nil
	}
	e := Entry{Home: home, Version: version, Major: major}
	if name != "" {
		r.removeName(name)
		e.Name = name
	} else {
		e.Name = r.uniqueName(e)
	}
	r.Entries = append(r.Entries, e)
	if err := r.Save(); err != nil {
		return nil, err
	}
	return &r.Entries[len(r.Entries)-1], nil
}

func (r *Registry) removeName(name string) {
	out := r.Entries[:0]
	for _, e := range r.Entries {
		if e.Name != name {
			out = append(out, e)
		}
	}
	r.Entries = out
}

// uniqueName: bare major when unique ("17"), else the full version
// ("17.0.9"), else version plus a short suffix ("17.0.9-2").
func (r *Registry) uniqueName(e Entry) string {
	taken := map[string]bool{}
	majorFree, versionFree := true, true
	majorStr := strconv.Itoa(e.Major)
	for _, o := range r.Entries {
		taken[o.Name] = true
		if o.Major == e.Major {
			majorFree = false
		}
		if o.Version == e.Version {
			versionFree = false
		}
	}
	if majorFree && !taken[majorStr] {
		return majorStr
	}
	if versionFree && !taken[e.Version] {
		return e.Version
	}
	for i := 2; ; i++ {
		n := fmt.Sprintf("%s-%d", e.Version, i)
		if !taken[n] {
			return n
		}
	}
}

var javaHomeEnvRe = regexp.MustCompile(`^JAVA_?(\d+)?_?HOME_?(\d+)?$`)

// candidates enumerates JDK home candidates from the scan sources in §12,
// deduped by normalized path.
func candidates(env []string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(home string) {
		if home == "" {
			return
		}
		// macOS layout: <dir>/Contents/Home
		if runtime.GOOS == "darwin" {
			if _, err := os.Stat(filepath.Join(home, "Contents", "Home", "bin", "java")); err == nil {
				home = filepath.Join(home, "Contents", "Home")
			}
		}
		if _, err := os.Stat(javaBinFor(home)); err != nil {
			return
		}
		key := normKey(home)
		if !seen[key] {
			seen[key] = true
			abs, _ := filepath.Abs(home)
			out = append(out, filepath.Clean(abs))
		}
	}

	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if ok && v != "" && javaHomeEnvRe.MatchString(k) {
			add(v)
		}
	}
	if p, err := exec.LookPath("java"); err == nil {
		if resolved, err := filepath.EvalSymlinks(p); err == nil {
			p = resolved
		}
		add(filepath.Dir(filepath.Dir(p)))
	}

	homeDir, _ := os.UserHomeDir()
	globDirs := []string{
		filepath.Join(homeDir, ".sdkman", "candidates", "java", "*"),
		filepath.Join(homeDir, ".jdks", "*"),
		"/usr/lib/jvm/*",
		filepath.Join(homeDir, ".local", "share", "mise", "installs", "java", "*"),
		filepath.Join(homeDir, ".asdf", "installs", "java", "*"),
	}
	if runtime.GOOS == "windows" {
		for _, envVar := range []string{"ProgramFiles", "ProgramFiles(x86)"} {
			if base := os.Getenv(envVar); base != "" {
				globDirs = append(globDirs, filepath.Join(base, "Java", "*"))
			}
		}
	}
	for _, pattern := range globDirs {
		matches, _ := filepath.Glob(pattern)
		for _, m := range matches {
			if st, err := os.Stat(m); err == nil && st.IsDir() {
				add(m)
			}
		}
	}
	return out
}

// Scan probes all candidates and registers every one with a working
// `java -version`; existing homes get their version refreshed.
func (r *Registry) Scan(env []string) (added, updated int, err error) {
	for _, home := range candidates(env) {
		version, major, err := Probe(home)
		if err != nil {
			continue
		}
		if e := r.byHome(home); e != nil {
			if e.Version != version || e.Major != major {
				e.Version, e.Major = version, major
				updated++
			}
			continue
		}
		e := Entry{Home: home, Version: version, Major: major}
		e.Name = r.uniqueName(e)
		r.Entries = append(r.Entries, e)
		added++
	}
	if err := r.Save(); err != nil {
		return added, updated, err
	}
	return added, updated, nil
}

// Resolve interprets an AppSpec jdk reference: absolute path | registry name
// | major version (highest patch wins). Absolute paths missing from the
// registry are probed once and cached.
func (r *Registry) Resolve(ref string) (*Entry, error) {
	if filepath.IsAbs(ref) {
		if e := r.byHome(ref); e != nil {
			return e, nil
		}
		e, err := r.Add(ref, "")
		if err != nil {
			return nil, fmt.Errorf("jdk %q: %w", ref, err)
		}
		return e, nil
	}
	for i := range r.Entries {
		if r.Entries[i].Name == ref {
			return &r.Entries[i], nil
		}
	}
	if isDigits(ref) {
		major, _ := strconv.Atoi(ref)
		var best *Entry
		for i := range r.Entries {
			e := &r.Entries[i]
			if e.Major == major && (best == nil || versionLess(best.Version, e.Version)) {
				best = e
			}
		}
		if best != nil {
			return best, nil
		}
	}
	return nil, fmt.Errorf("jdk %q not found (available: %s)", ref, r.Names())
}

func (r *Registry) Names() string {
	if len(r.Entries) == 0 {
		return "none; run `spm4a jdk scan`"
	}
	parts := make([]string, 0, len(r.Entries))
	for _, e := range r.Entries {
		parts = append(parts, fmt.Sprintf("%s(%s@%s)", e.Name, e.Version, e.Home))
	}
	return strings.Join(parts, ", ")
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// versionLess compares dotted versions component-wise ("25.0.1" > "25").
func versionLess(a, b string) bool {
	as := strings.FieldsFunc(a, func(r rune) bool { return r == '.' || r == '_' || r == '-' })
	bs := strings.FieldsFunc(b, func(r rune) bool { return r == '.' || r == '_' || r == '-' })
	for i := 0; i < len(as) && i < len(bs); i++ {
		an, _ := strconv.Atoi(leadingDigits(as[i]))
		bn, _ := strconv.Atoi(leadingDigits(bs[i]))
		if an != bn {
			return an < bn
		}
	}
	return len(as) < len(bs)
}

func leadingDigits(s string) string {
	for i, c := range s {
		if c < '0' || c > '9' {
			return s[:i]
		}
	}
	return s
}

// Bin returns the java binary path for a JDK home.
func Bin(home string) string { return javaBinFor(home) }

// ProbeBinMajor best-effort probes the major version of a java binary.
func ProbeBinMajor(javaBin string) int {
	dir := filepath.Dir(javaBin)
	home := filepath.Dir(dir)
	_, major, err := Probe(home)
	if err != nil {
		return 0
	}
	return major
}
