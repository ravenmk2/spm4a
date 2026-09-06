package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"spm4a/internal/state"
)

// resolveNamespace: --namespace/--ns > SPM4A_NAMESPACE > yaml namespace: >
// upward probe (.git / pom.xml / build.gradle, root dir name) > "default".
func resolveNamespace(yamlNS string) (string, error) {
	ns := flagNamespace
	if ns == "" {
		ns = os.Getenv("SPM4A_NAMESPACE")
	}
	if ns == "" {
		ns = yamlNS
	}
	if ns == "" {
		ns = probeNamespace()
	}
	if ns == "" {
		ns = "default"
	}
	if !state.ValidName(ns) {
		return "", fmt.Errorf("invalid namespace %q: must match [A-Za-z0-9._-]+", ns)
	}
	return ns, nil
}

func probeNamespace() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		for _, marker := range []string{".git", "pom.xml", "build.gradle"} {
			if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
				return filepath.Base(dir)
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
