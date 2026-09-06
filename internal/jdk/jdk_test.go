package jdk

import (
	"testing"
)

func TestParseVersion(t *testing.T) {
	cases := []struct {
		out     string
		version string
		major   int
	}{
		{`openjdk version "25.0.1" 2025-10-21 LTS`, "25.0.1", 25},
		{`openjdk version "17.0.9" 2023-10-17`, "17.0.9", 17},
		{`java version "1.8.0_392"`, "1.8.0_392", 8},
		{`openjdk version "21" 2023-09-19`, "21", 21},
		{`openjdk version "9-ea"`, "9-ea", 9},
	}
	for _, c := range cases {
		v, major, err := ParseVersion(c.out)
		if err != nil {
			t.Errorf("%q: %v", c.out, err)
			continue
		}
		if v != c.version || major != c.major {
			t.Errorf("%q -> (%q, %d), want (%q, %d)", c.out, v, major, c.version, c.major)
		}
	}
	if _, _, err := ParseVersion("no version here"); err == nil {
		t.Error("expected error for garbage output")
	}
}

func TestUniqueName(t *testing.T) {
	r := &Registry{}
	e := Entry{Version: "17.0.9", Major: 17}
	if n := r.uniqueName(e); n != "17" {
		t.Errorf("first 17 -> %q, want \"17\"", n)
	}
	r.Entries = append(r.Entries, Entry{Name: "17", Version: "17.0.9", Major: 17})
	e2 := Entry{Version: "17.0.7", Major: 17}
	if n := r.uniqueName(e2); n != "17.0.7" {
		t.Errorf("conflicting major -> %q, want \"17.0.7\"", n)
	}
	r.Entries = append(r.Entries, Entry{Name: "17.0.7", Version: "17.0.7", Major: 17})
	e3 := Entry{Version: "17.0.7", Major: 17}
	if n := r.uniqueName(e3); n != "17.0.7-2" {
		t.Errorf("conflicting version -> %q, want \"17.0.7-2\"", n)
	}
}

func TestResolveByNameAndMajor(t *testing.T) {
	r := &Registry{Entries: []Entry{
		{Name: "jdk8", Home: "/jdks/8", Version: "1.8.0_392", Major: 8},
		{Name: "jdk17a", Home: "/jdks/17.0.7", Version: "17.0.7", Major: 17},
		{Name: "jdk17b", Home: "/jdks/17.0.9", Version: "17.0.9", Major: 17},
	}}
	if e, err := r.Resolve("jdk17a"); err != nil || e.Home != "/jdks/17.0.7" {
		t.Errorf("Resolve(name jdk17a) = %v, %v", e, err)
	}
	// major reference picks the highest patch
	if e, err := r.Resolve("17"); err != nil || e.Home != "/jdks/17.0.9" {
		t.Errorf("Resolve(major 17) = %v, %v, want highest patch 17.0.9", e, err)
	}
	if _, err := r.Resolve("21"); err == nil {
		t.Error("Resolve(21) should fail")
	}
	if _, err := r.Resolve("jdk21"); err == nil {
		t.Error("Resolve(jdk21) should fail")
	}
}

func TestVersionLess(t *testing.T) {
	if !versionLess("17.0.7", "17.0.9") {
		t.Error("17.0.7 < 17.0.9 expected")
	}
	if versionLess("25.0.1", "25") {
		t.Error("25.0.1 should not be < 25")
	}
	if !versionLess("1.8.0_392", "11.0.2") {
		t.Error("8 < 11 expected")
	}
}

func TestJavaHomeEnvRe(t *testing.T) {
	for _, ok := range []string{"JAVA_HOME", "JAVA8_HOME", "JAVA_11_HOME", "JAVA_HOME_17", "JAVA_HOME21"} {
		if !javaHomeEnvRe.MatchString(ok) {
			t.Errorf("%s should match", ok)
		}
	}
	for _, no := range []string{"JAVA", "JAVA_HOME_DIR", "JAVAX_HOME", "JAVA_HOME_PATH"} {
		if javaHomeEnvRe.MatchString(no) {
			t.Errorf("%s should not match", no)
		}
	}
}
