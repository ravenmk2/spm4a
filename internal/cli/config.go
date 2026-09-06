package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"gopkg.in/yaml.v3"
)

const appFileName = "spm4a-app.yaml"

type appFileEntry struct {
	Name            string            `yaml:"name"`
	Workdir         string            `yaml:"workdir"`
	Launcher        string            `yaml:"launcher"`
	Jar             string            `yaml:"jar"`
	JDK             string            `yaml:"jdk"`
	Port            any               `yaml:"port"`
	Env             map[string]string `yaml:"env"`
	Args            []string          `yaml:"args"`
	HealthPath      string            `yaml:"health-path"`
	ShutdownTimeout string            `yaml:"shutdown-timeout"`
	RestartPolicy   string            `yaml:"restart-policy"`
	LogFile         string            `yaml:"log-file"`
	Xms             *string           `yaml:"xms"`
	Xmx             *string           `yaml:"xmx"`
	JvmOpts         []string          `yaml:"jvm-opts"`
}

type appFile struct {
	Namespace    string         `yaml:"namespace"`
	Apps         []appFileEntry `yaml:"apps"`
	appFileEntry `yaml:",inline"`
}

func loadAppFile(path string) (*appFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f appFile
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	e := f.appFileEntry
	if len(f.Apps) == 0 && (e.Name != "" || e.Workdir != "" || e.Launcher != "" ||
		e.Jar != "" || e.JDK != "" || e.Port != nil || len(e.Env) > 0 || len(e.Args) > 0) {
		f.Apps = []appFileEntry{e}
	}
	return &f, nil
}

// findAppFile loads the explicit -f file, else auto-discovers ./spm4a-app.yaml.
func findAppFile(explicit string) (string, *appFile, error) {
	if explicit != "" {
		f, err := loadAppFile(explicit)
		if err != nil {
			return "", nil, err
		}
		abs, err := filepath.Abs(explicit)
		if err != nil {
			return "", nil, err
		}
		return abs, f, nil
	}
	if _, err := os.Stat(appFileName); err == nil {
		abs, err := filepath.Abs(appFileName)
		if err != nil {
			return "", nil, err
		}
		f, err := loadAppFile(abs)
		if err != nil {
			return "", nil, err
		}
		return abs, f, nil
	}
	return "", nil, nil
}

// parsePort accepts an int, a numeric string, or "random" (0).
func parsePort(v any) (port int, set bool, err error) {
	switch t := v.(type) {
	case nil:
		return 0, false, nil
	case int:
		return t, true, nil
	case int64:
		return int(t), true, nil
	case string:
		if t == "random" {
			return 0, true, nil
		}
		n, perr := strconv.Atoi(t)
		if perr != nil {
			return 0, false, fmt.Errorf("invalid port %q: want a number or \"random\"", t)
		}
		return n, true, nil
	default:
		return 0, false, fmt.Errorf("invalid port value %v", v)
	}
}
