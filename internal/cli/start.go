package cli

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"spm4a/internal/ipc"
	"spm4a/internal/state"
)

type startFlags struct {
	file       string
	only       string
	name       string
	workdir    string
	launcher   string
	jar        string
	jdk        string
	port       string
	debug      string
	env        []string
	healthPath string
	logFile    string
	xms        string
	xmx        string
	jvmOpts    []string
	timeout    time.Duration
	noWait     bool
}

func newStartCmd() *cobra.Command {
	f := &startFlags{}
	c := &cobra.Command{
		Use:   "start [dir] [-- args...]",
		Short: "Start app(s) from spm4a-app.yaml or flags",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStart(cmd, f, args)
		},
	}
	fl := c.Flags()
	fl.StringVarP(&f.file, "file", "f", "", "path to spm4a-app.yaml")
	fl.StringVar(&f.only, "only", "", "start only the named app from the file")
	fl.StringVar(&f.name, "name", "", "app name (default: workdir base name)")
	fl.StringVar(&f.workdir, "workdir", "", "working directory")
	fl.StringVar(&f.launcher, "launcher", "", "launcher: jar|maven|gradle|custom")
	fl.StringVar(&f.jar, "jar", "", "jar path: absolute | relative to workdir | glob")
	fl.StringVar(&f.jdk, "jdk", "", "JDK home (M1: absolute path only)")
	fl.StringVar(&f.port, "port", "", "port (fixed number, or \"random\")")
	fl.StringVar(&f.debug, "debug", "", "enable JDWP debug agent (optional =port; bare = random)")
	fl.Lookup("debug").NoOptDefVal = "true"
	fl.StringArrayVar(&f.env, "env", nil, "environment variable K=V (repeatable)")
	fl.StringVar(&f.logFile, "log-file", "", "log file expression (default ./logs/${name}.log)")
	fl.StringVar(&f.healthPath, "health-path", "", "actuator health path (default /actuator/health)")
	fl.StringVar(&f.xms, "xms", "", "initial heap, e.g. 64M (default 32M; \"\" disables)")
	fl.StringVar(&f.xmx, "xmx", "", "max heap, e.g. 512M (default 256M; \"\" disables)")
	fl.StringArrayVar(&f.jvmOpts, "jvm-opt", nil, "extra JVM option (repeatable, one arg per flag)")
	fl.DurationVar(&f.timeout, "timeout", 60*time.Second, "ready wait timeout")
	fl.BoolVar(&f.noWait, "no-wait", false, "return immediately without waiting for readiness")
	return c
}

func runStart(cmd *cobra.Command, f *startFlags, args []string) error {
	var dir string
	var appArgs []string
	if dash := cmd.ArgsLenAtDash(); dash >= 0 {
		if dash > 1 {
			return usageErr("at most one [dir] argument before --")
		}
		if dash == 1 {
			dir = args[0]
		}
		appArgs = args[dash:]
	} else if len(args) > 0 {
		dir = args[0]
		appArgs = args[1:]
	}

	path, file, err := findAppFile(f.file)
	if err != nil {
		return err
	}

	var specs []ipc.StartSpec
	var portSet []bool
	var yamlNS string
	if file != nil {
		yamlNS = file.Namespace
		base := filepath.Dir(path)
		for _, e := range file.Apps {
			if f.only != "" && e.Name != f.only {
				continue
			}
			sp, set, err := specFromEntry(e, base)
			if err != nil {
				return err
			}
			specs = append(specs, sp)
			portSet = append(portSet, set)
		}
		if len(specs) == 0 {
			if f.only != "" {
				return usageErr("--only %q: no such app in %s", f.only, path)
			}
			return usageErr("no apps defined in %s", path)
		}
	} else {
		specs = []ipc.StartSpec{{}}
		portSet = []bool{false}
	}

	changed := func(name string) bool { return cmd.Flags().Changed(name) }
	cliEnv, err := parseEnvFlags(f.env)
	if err != nil {
		return err
	}
	for i := range specs {
		sp := &specs[i]
		if changed("name") {
			sp.Name = f.name
		}
		if changed("workdir") {
			sp.Workdir = f.workdir
		}
		if dir != "" {
			sp.Workdir = dir
		}
		if changed("launcher") {
			sp.Launcher = f.launcher
		}
		if changed("jar") {
			sp.Jar = f.jar
		}
		if changed("jdk") {
			sp.JDK = f.jdk
		}
		if changed("port") {
			p, set, err := parsePort(f.port)
			if err != nil {
				return usageErr("%v", err)
			}
			sp.Port = p
			portSet[i] = set
		}
		if changed("env") {
			if sp.Env == nil {
				sp.Env = map[string]string{}
			}
			for k, v := range cliEnv {
				sp.Env[k] = v
			}
		}
		if len(appArgs) > 0 {
			if sp.Launcher == "custom" {
				// for custom, trailing args after -- are the command argv
				sp.Command = appArgs
			} else {
				sp.Args = appArgs
			}
		}
		if changed("log-file") {
			sp.LogFile = f.logFile
		}
		if changed("health-path") {
			sp.HealthPath = f.healthPath
		}
		if changed("xms") {
			sp.Xms = &f.xms
		}
		if changed("xmx") {
			sp.Xmx = &f.xmx
		}
		if changed("jvm-opt") {
			sp.JvmOpts = f.jvmOpts
		}
		if changed("debug") {
			dbg, dport, err := parseDebug(f.debug)
			if err != nil {
				return usageErr("%v", err)
			}
			sp.Debug = dbg
			sp.DebugPort = dport
		}

		if sp.Workdir == "" {
			return usageErr("workdir is required (pass [dir], --workdir, or use %s)", appFileName)
		}
		abs, err := filepath.Abs(sp.Workdir)
		if err != nil {
			return usageErr("workdir: %v", err)
		}
		sp.Workdir = abs
		if sp.Name == "" {
			sp.Name = filepath.Base(abs)
		}
		if !portSet[i] {
			sp.Port = 8080
		}
	}

	ns, err := resolveNamespace(yamlNS)
	if err != nil {
		return err
	}
	for i := range specs {
		specs[i].Namespace = ns
	}

	c, err := rpcClient(cmd.Context())
	if err != nil {
		return err
	}
	wait := !f.noWait
	params := ipc.StartParams{Specs: specs, Wait: &wait, Timeout: f.timeout.String()}
	if len(specs) == 1 {
		params.Spec = &specs[0]
		params.Specs = nil
	}
	var res struct {
		Apps []*state.App `json:"apps"`
	}
	if err := c.Call(cmd.Context(), "app.start", params, &res); err != nil {
		return err
	}
	if flagJSON {
		return printJSON(res)
	}
	for _, a := range res.Apps {
		fmt.Printf("%s: %s (namespace %s, port %d, pid %d)\n",
			a.Spec.Name, a.Status, a.Spec.Namespace, a.ActualPort, a.PID)
	}
	return nil
}

func specFromEntry(e appFileEntry, base string) (ipc.StartSpec, bool, error) {
	var sp ipc.StartSpec
	if e.Workdir == "" {
		return sp, false, usageErr("%s: app %q: workdir is required", appFileName, e.Name)
	}
	workdir := filepath.FromSlash(e.Workdir)
	if !filepath.IsAbs(workdir) {
		workdir = filepath.Join(base, workdir)
	}
	port, set, err := parsePort(e.Port)
	if err != nil {
		return sp, false, usageErr("%s: app %q: %v", appFileName, e.Name, err)
	}
	debug, debugPort, err := parseDebug(e.Debug)
	if err != nil {
		return sp, false, usageErr("%s: app %q: %v", appFileName, e.Name, err)
	}
	sp = ipc.StartSpec{
		Name:            e.Name,
		Workdir:         workdir,
		Launcher:        e.Launcher,
		Jar:             e.Jar,
		Command:         e.Command,
		JDK:             e.JDK,
		Port:            port,
		Env:             e.Env,
		Args:            e.Args,
		HealthPath:      e.HealthPath,
		LogFile:         e.LogFile,
		ShutdownTimeout: e.ShutdownTimeout,
		Xms:             e.Xms,
		Xmx:             e.Xmx,
		JvmOpts:         e.JvmOpts,
		Debug:           debug,
		DebugPort:       debugPort,
	}
	return sp, set, nil
}

func parseEnvFlags(pairs []string) (map[string]string, error) {
	out := map[string]string{}
	for _, p := range pairs {
		k, v, ok := strings.Cut(p, "=")
		if !ok || k == "" {
			return nil, usageErr("--env %q: want K=V", p)
		}
		out[k] = v
	}
	return out, nil
}
