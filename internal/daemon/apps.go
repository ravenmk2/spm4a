package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"spm4a/internal/health"
	"spm4a/internal/inject"
	"spm4a/internal/ipc"
	"spm4a/internal/launcher"
	"spm4a/internal/proc"
	"spm4a/internal/state"
)

// errEarlyExit marks a process that exited before becoming ready; with a
// random port this is treated as a candidate-port failure and retried once.
var errEarlyExit = errors.New("process exited before ready")

// launch resolves everything, acquires a port (pool for random), spawns the
// process and optionally blocks until the app is ready (§10.1).
func (d *Daemon) launch(app *state.App, wait bool, timeout time.Duration) error {
	spec := &app.Spec

	tool, err := d.resolveTool(spec)
	if err != nil {
		return err
	}
	logExpr := spec.LogFile
	if logExpr == "" {
		logExpr = "./logs/${name}.log"
	}
	if err := launcher.ValidateLogExpr(logExpr); err != nil {
		return ipc.NewError(ipc.CodeInvalidParams, err.Error())
	}
	jvmOpts := launcher.BuildJvmOpts(spec.Xms, spec.Xmx, spec.JvmOpts)
	vars := map[string]string{
		"name":      spec.Name,
		"namespace": spec.Namespace,
		"workdir":   spec.Workdir,
		"ts":        time.Now().Format("20060102-150405"),
	}

	randomPort := spec.Port == 0
	attempts := 1
	if randomPort && wait {
		attempts = 2
	}
	for attempt := 0; ; attempt++ {
		port := spec.Port
		if randomPort {
			port, err = d.pool.Acquire()
			if err != nil {
				return ipc.NewError(ipc.CodeInternal, "port pool: "+err.Error())
			}
		} else {
			ln, lerr := net.Listen("tcp", fmt.Sprintf(":%d", port))
			if lerr != nil {
				return ipc.NewError(ipc.CodePortConflict, fmt.Sprintf("port %d is already in use", port))
			}
			_ = ln.Close()
		}

		p, err := d.startProcess(app, port, tool, jvmOpts, logExpr, vars)
		if err != nil {
			if randomPort {
				d.pool.Release(port)
			}
			return err
		}
		if !wait {
			// Readiness (and reservation release) happens in the background.
			go func() {
				if randomPort {
					defer d.pool.Release(port)
				}
				if err := d.waitReady(app, p, timeout); err != nil && !errors.Is(err, errEarlyExit) {
					d.log.Warn("background ready check failed", "app", spec.Name, "err", err)
				}
			}()
			return nil
		}

		err = d.waitReady(app, p, timeout)
		if randomPort {
			d.pool.Release(port)
		}
		if err != nil && errors.Is(err, errEarlyExit) && randomPort && attempt+1 < attempts {
			d.log.Info("app exited before ready; retrying with a new port", "app", spec.Name, "port", port)
			continue
		}
		if errors.Is(err, errEarlyExit) {
			return ipc.NewError(ipc.CodeInvalidState, fmt.Sprintf(
				"app %q exited during startup; see log %s", app.Spec.Name, app.LogPath))
		}
		return err
	}
}

// toolInfo holds the per-launcher resolution result used to build the command.
type toolInfo struct {
	javaBin string // jar launcher: resolved java binary; maven/gradle: unused
	jarPath string // jar launcher only
	toolBin string // maven/gradle binary
}

// resolveTool validates and resolves launcher-specific bits once per launch.
func (d *Daemon) resolveTool(spec *state.Spec) (*toolInfo, error) {
	switch spec.Launcher {
	case "jar":
		jarPath, err := launcher.ResolveJar(spec.Workdir, spec.Jar)
		if err != nil {
			return nil, ipc.NewError(ipc.CodeInvalidParams, err.Error())
		}
		javaBin, err := launcher.ResolveJava(spec.JDK)
		if err != nil {
			return nil, ipc.NewError(ipc.CodeInvalidParams, err.Error())
		}
		return &toolInfo{javaBin: javaBin, jarPath: jarPath}, nil
	case "maven":
		mvn, err := launcher.ResolveMaven()
		if err != nil {
			return nil, ipc.NewError(ipc.CodeInvalidParams, err.Error())
		}
		return &toolInfo{toolBin: mvn}, nil
	case "gradle":
		g, err := launcher.ResolveGradle(spec.Workdir)
		if err != nil {
			return nil, ipc.NewError(ipc.CodeInvalidParams, err.Error())
		}
		return &toolInfo{toolBin: g}, nil
	}
	return nil, ipc.NewError(ipc.CodeInvalidParams, "unsupported launcher: "+spec.Launcher)
}

// buildCommand constructs the launcher's *exec.Cmd for one start attempt.
func (d *Daemon) buildCommand(spec *state.Spec, tool *toolInfo, jvmOpts []string) (*exec.Cmd, error) {
	switch spec.Launcher {
	case "jar":
		return launcher.BuildJarCommand(tool.javaBin, tool.jarPath, jvmOpts, spec.Args), nil
	case "maven":
		return launcher.BuildMavenCommand(tool.toolBin, jvmOpts, spec.Args), nil
	case "gradle":
		init := ""
		if len(jvmOpts) > 0 {
			init = filepath.Join(d.ep.RunDir(),
				fmt.Sprintf("spm4a-init-%s-%s.gradle", spec.Namespace, spec.Name))
			if err := os.WriteFile(init, []byte(launcher.GradleInitScript(jvmOpts)), 0o644); err != nil {
				return nil, ipc.NewError(ipc.CodeInternal, "write gradle init script: "+err.Error())
			}
		}
		return launcher.BuildGradleCommand(tool.toolBin, spec.Args, init), nil
	}
	return nil, ipc.NewError(ipc.CodeInvalidParams, "unsupported launcher: "+spec.Launcher)
}

// startProcess builds env/command, spawns the child, and registers runtime
// state. The app must already be in the store.
func (d *Daemon) startProcess(app *state.App, port int, tool *toolInfo, jvmOpts []string, logExpr string, vars map[string]string) (*proc.Proc, error) {
	spec := &app.Spec

	env := map[string]string{}
	for _, e := range os.Environ() {
		if k, v, ok := strings.Cut(e, "="); ok {
			env[k] = v
		}
	}
	for k, v := range spec.Env {
		env[k] = v
	}
	javaBin := tool.javaBin
	if spec.Launcher != "jar" && spec.JDK != "" {
		// maven/gradle run the build tool; hand the JDK over via JAVA_HOME (§12)
		env["JAVA_HOME"] = spec.JDK
		javaBin = filepath.Join(spec.JDK, "bin", launcher.JavaExeName())
	}
	env, injected, err := inject.BuildSpringEnv(env, port)
	if err != nil {
		return nil, ipc.NewError(ipc.CodeInvalidParams, err.Error())
	}

	cmd, err := d.buildCommand(spec, tool, jvmOpts)
	if err != nil {
		return nil, err
	}
	cmd.Dir = spec.Workdir
	cmd.Env = envList(env)

	var lw *proc.LogWriter
	var logPath string
	if launcher.LogExprHasVar(logExpr, "pid") {
		pathFor := func(pid int) (string, error) {
			v := map[string]string{}
			for k, val := range vars {
				v[k] = val
			}
			v["pid"] = strconv.Itoa(pid)
			return launcher.ResolveLogPath(logExpr, spec.Workdir, v), nil
		}
		lw = proc.NewLogWriter(pathFor)
		cmd.Stdout = lw
		cmd.Stderr = lw
	} else {
		logPath = launcher.ResolveLogPath(logExpr, spec.Workdir, vars)
		if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
			return nil, ipc.NewError(ipc.CodeInternal, "create log dir: "+err.Error())
		}
		f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return nil, ipc.NewError(ipc.CodeInternal, "open log file: "+err.Error())
		}
		defer f.Close()
		cmd.Stdout = f
		cmd.Stderr = f
	}

	p, err := proc.Start(cmd)
	if err != nil {
		return nil, ipc.NewError(ipc.CodeInternal, "start process: "+err.Error())
	}
	if lw != nil {
		lw.SetPID(p.PID())
		logPath = launcher.ResolveLogPath(logExpr, spec.Workdir, map[string]string{
			"name": vars["name"], "namespace": vars["namespace"], "workdir": vars["workdir"],
			"ts": vars["ts"], "pid": strconv.Itoa(p.PID()),
		})
	}

	key := d.key(spec.Namespace, spec.Name)
	d.mu.Lock()
	app.JavaBin = javaBin
	app.ResolvedJar = tool.jarPath
	app.ResolvedJvmOpts = jvmOpts
	app.LogPath = logPath
	app.Injected = injected
	app.PID = p.PID()
	app.ActualPort = port
	app.Status = state.StatusStarting
	app.LastExit = nil
	d.procs[key] = p
	if err := d.store.Save(spec.Namespace); err != nil {
		d.log.Warn("persist state", "err", err)
	}
	d.mu.Unlock()
	d.publish(EventAppStatus, spec.Namespace, spec.Name, app)
	d.refreshApps()

	go d.watch(key, p)
	return p, nil
}

// waitReady implements §10.1: TCP connect first, then poll the actuator
// health endpoint until UP. On timeout the process is killed.
func (d *Daemon) waitReady(app *state.App, p *proc.Proc, timeout time.Duration) error {
	addr := fmt.Sprintf("127.0.0.1:%d", app.ActualPort)
	healthPath := app.Spec.HealthPath
	deadline := time.Now().Add(timeout)
	tcpUp := false
	for {
		if p.Exited() {
			code := proc.ExitCode(p.Err())
			d.failApp(app, code)
			return fmt.Errorf("%w (exit code %d)", errEarlyExit, code)
		}
		if !tcpUp {
			conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
			if err == nil {
				conn.Close()
				tcpUp = true
			}
		} else {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			up := health.IsUp(ctx, app.ActualPort, healthPath)
			cancel()
			if up {
				d.mu.Lock()
				app.Status = state.StatusReady
				_ = d.store.Save(app.Spec.Namespace)
				d.mu.Unlock()
				d.publish(EventAppStatus, app.Spec.Namespace, app.Spec.Name, app)
				d.startProbe(d.key(app.Spec.Namespace, app.Spec.Name))
				return nil
			}
		}
		if time.Now().After(deadline) {
			_ = p.Kill()
			select {
			case <-p.Done():
			case <-time.After(5 * time.Second):
			}
			d.failApp(app, -1)
			return ipc.NewError(ipc.CodeReadyTimeout, fmt.Sprintf(
				"app %q did not become ready on port %d within %s; see log %s",
				app.Spec.Name, app.ActualPort, timeout, app.LogPath))
		}
		time.Sleep(300 * time.Millisecond)
	}
}

func (d *Daemon) failApp(app *state.App, code int) {
	d.mu.Lock()
	switch app.Status {
	case state.StatusStarting, state.StatusReady, state.StatusUnready:
		app.Status = state.StatusError
		app.PID = 0
		app.LastExit = &state.ExitInfo{Code: code, At: time.Now()}
		_ = d.store.Save(app.Spec.Namespace)
	default: // stopping/stopped: the stop flow owns the final state
	}
	d.mu.Unlock()
	d.publish(EventAppStatus, app.Spec.Namespace, app.Spec.Name, app)
	d.refreshApps()
}

func (d *Daemon) watch(key string, p *proc.Proc) {
	<-p.Done()
	code := proc.ExitCode(p.Err())
	d.mu.Lock()
	if d.procs[key] != p {
		d.mu.Unlock()
		return // superseded by a retry/restart; the new watch owns the app
	}
	delete(d.procs, key)
	d.cancelProbeLocked(key)
	ns, name, _ := strings.Cut(key, "/")
	app, ok := d.store.Get(ns, name)
	if ok {
		app.LastExit = &state.ExitInfo{Code: code, At: time.Now()}
		switch app.Status {
		case state.StatusStopping, state.StatusStopped:
			app.Status = state.StatusStopped
		default:
			app.Status = state.StatusError
		}
		app.PID = 0
		_ = d.store.Save(ns)
	}
	d.mu.Unlock()
	if ok {
		d.publish(EventAppStatus, ns, name, app)
	}
	d.refreshApps()
}

// probeLoop is the §10.1 liveness probe: every 5s, three consecutive
// failures mark the app unready (never killed); recovery marks it ready.
func (d *Daemon) probeLoop(ctx context.Context, key string, port int, healthPath string) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	fails := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		pctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		up := health.IsUp(pctx, port, healthPath)
		cancel()
		if up {
			fails = 0
		} else {
			fails++
		}
		d.mu.Lock()
		app, ok := d.appByKey(key)
		switch {
		case !ok:
			d.mu.Unlock()
			return
		case up && app.Status == state.StatusUnready:
			app.Status = state.StatusReady
			_ = d.store.Save(app.Spec.Namespace)
			d.mu.Unlock()
			d.publish(EventAppStatus, app.Spec.Namespace, app.Spec.Name, app)
		case !up && fails >= 3 && app.Status == state.StatusReady:
			app.Status = state.StatusUnready
			_ = d.store.Save(app.Spec.Namespace)
			d.mu.Unlock()
			d.publish(EventAppStatus, app.Spec.Namespace, app.Spec.Name, app)
		default:
			d.mu.Unlock()
		}
	}
}

func (d *Daemon) startProbe(key string) {
	d.mu.Lock()
	app, ok := d.appByKey(key)
	if !ok || app.ActualPort == 0 {
		d.mu.Unlock()
		return
	}
	port, healthPath := app.ActualPort, app.Spec.HealthPath
	if old, ok := d.probes[key]; ok {
		old()
	}
	ctx, cancel := context.WithCancel(context.Background())
	d.probes[key] = cancel
	d.mu.Unlock()
	go d.probeLoop(ctx, key, port, healthPath)
}

func (d *Daemon) cancelProbeLocked(key string) {
	if cancel, ok := d.probes[key]; ok {
		cancel()
		delete(d.probes, key)
	}
}

func (d *Daemon) appByKey(key string) (*state.App, bool) {
	ns, name, ok := strings.Cut(key, "/")
	if !ok {
		return nil, false
	}
	return d.store.Get(ns, name)
}

// stopApp implements §10.2: actuator /shutdown first, signal fallback when
// HTTP fails, hard kill after the shutdown timeout. Idempotent.
func (d *Daemon) stopApp(app *state.App, timeout time.Duration, now bool) {
	if !app.Active() {
		return
	}
	ns, name := app.Spec.Namespace, app.Spec.Name
	key := d.key(ns, name)
	d.mu.Lock()
	app.Status = state.StatusStopping
	_ = d.store.Save(ns)
	p := d.procs[key]
	pid := app.PID
	port := app.ActualPort
	d.cancelProbeLocked(key)
	d.mu.Unlock()
	d.publish(EventAppStatus, ns, name, app)

	deadline := time.Now().Add(timeout)
	switch {
	case now:
		killProc(p, pid)
	case port == 0 || (p == nil && pid == 0):
		terminateProc(p, pid)
		waitOrKill(p, pid, time.Until(deadline))
	default:
		sctx, cancel := context.WithTimeout(context.Background(), minDuration(5*time.Second, timeout))
		err := health.Shutdown(sctx, port)
		cancel()
		if err != nil {
			d.log.Info("actuator shutdown failed, falling back to signals", "app", name, "err", err)
			terminateProc(p, pid)
		}
		waitOrKill(p, pid, time.Until(deadline))
	}

	d.mu.Lock()
	app.Status = state.StatusStopped
	app.PID = 0
	_ = d.store.Save(ns)
	d.mu.Unlock()
	d.publish(EventAppStatus, ns, name, app)
	d.refreshApps()
}

// waitOrKill waits for exit, then force-kills as the final fallback.
func waitOrKill(p *proc.Proc, pid int, d time.Duration) {
	if d < 0 {
		d = 0
	}
	if waitProcExit(p, pid, d) {
		return
	}
	killProc(p, pid)
	waitProcExit(p, pid, 5*time.Second)
}

func waitProcExit(p *proc.Proc, pid int, d time.Duration) bool {
	if p != nil {
		select {
		case <-p.Done():
			return true
		case <-time.After(d):
			return false
		}
	}
	if pid != 0 {
		return proc.WaitDead(pid, d)
	}
	return true
}

func terminateProc(p *proc.Proc, pid int) {
	if p != nil {
		_ = p.Terminate()
		return
	}
	if pid != 0 {
		_ = proc.TerminatePID(pid)
	}
}

func killProc(p *proc.Proc, pid int) {
	if p != nil {
		_ = p.Kill()
		return
	}
	if pid != 0 {
		_ = proc.KillPID(pid)
	}
}

func (d *Daemon) stopAll() {
	d.mu.Lock()
	var apps []*state.App
	for _, a := range d.store.List("", true) {
		if a.Active() {
			apps = append(apps, a)
		}
	}
	d.mu.Unlock()
	for _, a := range apps {
		timeout := a.Spec.ShutdownTimeout
		if timeout <= 0 {
			timeout = 15 * time.Second
		}
		d.stopApp(a, timeout, false)
	}
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func envList(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}
