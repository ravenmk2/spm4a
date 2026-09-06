package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"spm4a/internal/ipc"
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

// resolveTarget determines the concrete namespace for a single-app command.
// With --all-namespace the name is resolved globally via app.list: exactly
// one match wins, zero is app-not-found, many is an ambiguity usage error.
func resolveTarget(ctx context.Context, cl *ipc.Client, name string) (string, error) {
	if !flagAllNs {
		return resolveNamespace("")
	}
	var res struct {
		Apps []*state.App `json:"apps"`
	}
	if err := cl.Call(ctx, "app.list", ipc.ListParams{All: true}, &res); err != nil {
		return "", err
	}
	var matches []*state.App
	for _, a := range res.Apps {
		if a.Spec.Name == name {
			matches = append(matches, a)
		}
	}
	switch len(matches) {
	case 0:
		return "", &ipc.Error{Code: ipc.CodeAppNotFound,
			Message: fmt.Sprintf("app %q not found in any namespace", name)}
	case 1:
		return matches[0].Spec.Namespace, nil
	}
	cands := make([]string, 0, len(matches))
	for _, m := range matches {
		cands = append(cands, fmt.Sprintf("%s/%s (%s)", m.Spec.Namespace, m.Spec.Name, m.Status))
	}
	return "", usageErr("ambiguous app name %q across namespaces: %s", name, strings.Join(cands, ", "))
}
