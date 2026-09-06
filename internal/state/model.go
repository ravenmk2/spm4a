package state

import (
	"regexp"
	"time"
)

const (
	StatusStarting = "starting"
	StatusReady    = "ready"
	StatusUnready  = "unready"
	StatusStopping = "stopping"
	StatusStopped  = "stopped"
	StatusError    = "error"
)

var nameRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func ValidName(s string) bool { return nameRe.MatchString(s) }

type Spec struct {
	Name            string            `json:"name"`
	Namespace       string            `json:"namespace"`
	Workdir         string            `json:"workdir"`
	Launcher        string            `json:"launcher"`
	Jar             string            `json:"jar,omitempty"`
	Command         []string          `json:"command,omitempty"` // launcher=custom: argv, executed in workdir
	JDK             string            `json:"jdk,omitempty"`
	Port            int               `json:"port"` // 0 = random from the daemon pool
	Env             map[string]string `json:"env,omitempty"`
	Args            []string          `json:"args,omitempty"`
	HealthPath      string            `json:"healthPath"`
	ShutdownTimeout time.Duration     `json:"shutdownTimeout"`
	LogFile         string            `json:"logFile,omitempty"`
	Xms             string            `json:"xms"` // "" = not injected (default materialized at start)
	Xmx             string            `json:"xmx"`
	JvmOpts         []string          `json:"jvmOpts,omitempty"`
	Debug           bool              `json:"debug,omitempty"`
	DebugPort       int               `json:"debugPort,omitempty"` // 0 = random from pool
}

type ExitInfo struct {
	Code int       `json:"code"`
	At   time.Time `json:"at"`
}

type App struct {
	Spec            Spec           `json:"spec"`
	PID             int            `json:"pid,omitempty"`
	Status          string         `json:"status"`
	ActualPort      int            `json:"actualPort,omitempty"`
	DebugPort       int            `json:"debugPort,omitempty"`
	JavaBin         string         `json:"javaBin,omitempty"`
	ResolvedJar     string         `json:"resolvedJar,omitempty"`
	ResolvedJvmOpts []string       `json:"resolvedJvmOpts,omitempty"`
	LogPath         string         `json:"logPath,omitempty"`
	Injected        map[string]any `json:"injected,omitempty"`
	Restarts        int            `json:"restarts"`
	StartedAt       time.Time      `json:"startedAt,omitempty"`
	LastExit        *ExitInfo      `json:"lastExit,omitempty"`
}

func (a *App) Active() bool {
	switch a.Status {
	case StatusStarting, StatusReady, StatusUnready, StatusStopping:
		return true
	}
	return false
}

// Snapshot returns a deep-enough copy safe to marshal outside the daemon lock.
func (a *App) Snapshot() *App {
	cp := *a
	cp.Spec.Env = cloneMap(a.Spec.Env)
	cp.Spec.Args = append([]string(nil), a.Spec.Args...)
	cp.Spec.Command = append([]string(nil), a.Spec.Command...)
	cp.Spec.JvmOpts = append([]string(nil), a.Spec.JvmOpts...)
	cp.ResolvedJvmOpts = append([]string(nil), a.ResolvedJvmOpts...)
	if a.Injected != nil {
		m := make(map[string]any, len(a.Injected))
		for k, v := range a.Injected {
			m[k] = v
		}
		cp.Injected = m
	}
	if a.LastExit != nil {
		le := *a.LastExit
		cp.LastExit = &le
	}
	return &cp
}

func cloneMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
