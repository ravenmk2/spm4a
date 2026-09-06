package ipc

// Protocol types (camelCase on the wire).

type StartSpec struct {
	Name            string            `json:"name"`
	Namespace       string            `json:"namespace"`
	Workdir         string            `json:"workdir"`
	Launcher        string            `json:"launcher,omitempty"`
	Jar             string            `json:"jar,omitempty"`
	JDK             string            `json:"jdk,omitempty"`
	Port            int               `json:"port,omitempty"` // 0 = random from the daemon pool
	Env             map[string]string `json:"env,omitempty"`
	Args            []string          `json:"args,omitempty"`
	HealthPath      string            `json:"healthPath,omitempty"`
	LogFile         string            `json:"logFile,omitempty"`
	ShutdownTimeout string            `json:"shutdownTimeout,omitempty"`
	Xms             *string           `json:"xms,omitempty"` // nil = default 32M; "" = disable injection
	Xmx             *string           `json:"xmx,omitempty"` // nil = default 256M; "" = disable injection
	JvmOpts         []string          `json:"jvmOpts,omitempty"`
	Debug           bool              `json:"debug,omitempty"`
	DebugPort       int               `json:"debugPort,omitempty"` // 0 = random from pool
}

type StartParams struct {
	Spec    *StartSpec  `json:"spec,omitempty"`
	Specs   []StartSpec `json:"specs,omitempty"`
	Wait    *bool       `json:"wait,omitempty"`
	Timeout string      `json:"timeout,omitempty"`
}

type NameParams struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

type StopParams struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Now       bool   `json:"now,omitempty"`
	Timeout   string `json:"timeout,omitempty"`
}

type RestartParams struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Timeout   string `json:"timeout,omitempty"`
}

type ReloadParams struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Timeout   string `json:"timeout,omitempty"`
}

// ReloadResult is the app.reload result: build outcome plus the health
// status observed afterwards.
type ReloadResult struct {
	ExitCode  int    `json:"exitCode"`
	Output    string `json:"output"` // tail of the build output
	Health    any    `json:"health,omitempty"`
	AppStatus string `json:"appStatus"`
}

type ListParams struct {
	Namespace string `json:"namespace,omitempty"`
	All       bool   `json:"all,omitempty"`
}

type LogsParams struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Lines     int    `json:"lines,omitempty"`
}

type SubscribeParams struct {
	Namespace string `json:"namespace,omitempty"`
}

type ShutdownParams struct {
	All bool `json:"all,omitempty"`
}

type PingResult struct {
	Version   string `json:"version"`
	PID       int    `json:"pid"`
	StartedAt string `json:"startedAt"`
}
