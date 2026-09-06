package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"spm4a/internal/health"
	"spm4a/internal/ipc"
	"spm4a/internal/launcher"
	"spm4a/internal/proc"
	"spm4a/internal/state"
)

func (d *Daemon) register() {
	d.srv.Handle("daemon.ping", d.ping)
	d.srv.Handle("daemon.shutdown", d.shutdownRPC)
	d.srv.Handle("app.start", d.appStart)
	d.srv.Handle("app.stop", d.appStop)
	d.srv.Handle("app.restart", d.appRestart)
	d.srv.Handle("app.reload", d.appReload)
	d.srv.Handle("app.list", d.appList)
	d.srv.Handle("app.status", d.appStatus)
	d.srv.Handle("app.health", d.appHealth)
	d.srv.Handle("app.delete", d.appDelete)
	d.srv.Handle("logs.get", d.logsGet)
	d.srv.Handle("logs.follow", d.logsFollow)
	d.srv.Handle("events.subscribe", d.eventsSubscribe)
}

func (d *Daemon) ping(context.Context, json.RawMessage) (any, error) {
	return ipc.PingResult{
		Version:   Version,
		PID:       os.Getpid(),
		StartedAt: d.startedAt.Format(time.RFC3339),
	}, nil
}

func (d *Daemon) shutdownRPC(_ context.Context, raw json.RawMessage) (any, error) {
	var p ipc.ShutdownParams
	if len(raw) > 0 {
		if err := ipc.DecodeParams(raw, &p); err != nil {
			return nil, err
		}
	}
	if p.All {
		d.stopAll()
	}
	d.idle.forceShutdown()
	return map[string]any{"status": "shutting-down"}, nil
}

func (d *Daemon) appStart(_ context.Context, raw json.RawMessage) (any, error) {
	var p ipc.StartParams
	if err := ipc.DecodeParams(raw, &p); err != nil {
		return nil, err
	}
	var specs []ipc.StartSpec
	if p.Spec != nil {
		specs = append(specs, *p.Spec)
	}
	specs = append(specs, p.Specs...)
	if len(specs) == 0 {
		return nil, ipc.ErrParams("spec or specs is required")
	}
	wait := true
	if p.Wait != nil {
		wait = *p.Wait
	}
	timeout := 60 * time.Second
	if p.Timeout != "" {
		t, err := time.ParseDuration(p.Timeout)
		if err != nil || t <= 0 {
			return nil, ipc.ErrParams("invalid timeout: " + p.Timeout)
		}
		timeout = t
	}

	var views []*state.App
	var firstErr error
	for i := range specs {
		app, err := d.startOne(&specs[i], wait, timeout)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		views = append(views, app)
	}
	if firstErr != nil {
		return nil, firstErr
	}
	return map[string]any{"apps": views}, nil
}

func (d *Daemon) startOne(sp *ipc.StartSpec, wait bool, timeout time.Duration) (*state.App, error) {
	spec, err := specFromRPC(sp)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	if _, ok := d.store.Get(spec.Namespace, spec.Name); ok {
		d.mu.Unlock()
		return nil, ipc.NewError(ipc.CodeAppExists,
			fmt.Sprintf("app %q already exists in namespace %q (use restart, or rm first)", spec.Name, spec.Namespace))
	}
	app := &state.App{Spec: *spec, Status: state.StatusStarting}
	if err := d.store.Put(app); err != nil {
		d.mu.Unlock()
		return nil, ipc.NewError(ipc.CodeInternal, "persist state: "+err.Error())
	}
	d.mu.Unlock()
	d.refreshApps()

	if err := d.launch(app, wait, timeout); err != nil {
		d.mu.Lock()
		if app.Status == state.StatusStarting {
			app.Status = state.StatusError
			_ = d.store.Save(spec.Namespace)
		}
		d.mu.Unlock()
		d.publish(EventAppStatus, spec.Namespace, spec.Name, app)
		d.refreshApps()
		return nil, err
	}
	d.mu.Lock()
	view := app.Snapshot()
	d.mu.Unlock()
	return view, nil
}

func specFromRPC(sp *ipc.StartSpec) (*state.Spec, error) {
	if sp.Name == "" {
		return nil, ipc.ErrParams("name is required")
	}
	if !state.ValidName(sp.Name) {
		return nil, ipc.ErrParams(fmt.Sprintf("invalid app name %q: must match [A-Za-z0-9._-]+", sp.Name))
	}
	if !state.ValidName(sp.Namespace) {
		return nil, ipc.ErrParams(fmt.Sprintf("invalid namespace %q: must match [A-Za-z0-9._-]+", sp.Namespace))
	}
	if sp.Workdir == "" {
		return nil, ipc.ErrParams("workdir is required")
	}
	if st, err := os.Stat(sp.Workdir); err != nil || !st.IsDir() {
		return nil, ipc.ErrParams("workdir does not exist or is not a directory: " + sp.Workdir)
	}
	launcherKind := sp.Launcher
	if launcherKind == "" {
		launcherKind = "jar"
	}
	switch launcherKind {
	case "jar", "maven", "gradle":
	case "custom":
		return nil, ipc.ErrParams("custom launcher is not supported yet (M3: jar|maven|gradle)")
	default:
		return nil, ipc.ErrParams(fmt.Sprintf("unknown launcher %q (want jar|maven|gradle)", sp.Launcher))
	}
	if launcherKind == "jar" && sp.Jar == "" {
		return nil, ipc.ErrParams("jar is required for launcher=jar")
	}
	if sp.Port < 0 || sp.Port > 65535 {
		return nil, ipc.ErrParams(fmt.Sprintf("invalid port %d", sp.Port))
	}
	if sp.DebugPort < 0 || sp.DebugPort > 65535 {
		return nil, ipc.ErrParams(fmt.Sprintf("invalid debugPort %d", sp.DebugPort))
	}
	healthPath := sp.HealthPath
	if healthPath == "" {
		healthPath = "/actuator/health"
	}
	if !strings.HasPrefix(healthPath, "/") {
		return nil, ipc.ErrParams("healthPath must start with /: " + healthPath)
	}
	xms, err := materializeHeap("xms", sp.Xms, "32M")
	if err != nil {
		return nil, err
	}
	xmx, err := materializeHeap("xmx", sp.Xmx, "256M")
	if err != nil {
		return nil, err
	}
	shutdownTimeout := 15 * time.Second
	if sp.ShutdownTimeout != "" {
		t, err := time.ParseDuration(sp.ShutdownTimeout)
		if err != nil || t <= 0 {
			return nil, ipc.ErrParams("invalid shutdownTimeout: " + sp.ShutdownTimeout)
		}
		shutdownTimeout = t
	}
	return &state.Spec{
		Name:            sp.Name,
		Namespace:       sp.Namespace,
		Workdir:         sp.Workdir,
		Launcher:        launcherKind,
		Jar:             sp.Jar,
		JDK:             sp.JDK,
		Port:            sp.Port,
		Env:             sp.Env,
		Args:            sp.Args,
		HealthPath:      healthPath,
		ShutdownTimeout: shutdownTimeout,
		LogFile:         sp.LogFile,
		Xms:             xms,
		Xmx:             xmx,
		JvmOpts:         sp.JvmOpts,
		Debug:           sp.Debug,
		DebugPort:       sp.DebugPort,
	}, nil
}

// materializeHeap: nil = built-in default, "" = injection disabled, else a
// validated heap size per §8.4.
func materializeHeap(name string, v *string, def string) (string, error) {
	if v == nil {
		return def, nil
	}
	if *v == "" {
		return "", nil
	}
	if !launcher.ValidHeapSize(*v) {
		return "", ipc.ErrParams(fmt.Sprintf("invalid %s %q: must match ^\\d+[kKmMgG]$ (or \"\" to disable)", name, *v))
	}
	return *v, nil
}

func (d *Daemon) lookupApp(ns, name string) (*state.App, *ipc.Error) {
	if !state.ValidName(ns) {
		return nil, ipc.ErrParams(fmt.Sprintf("invalid namespace %q", ns))
	}
	d.mu.Lock()
	app, ok := d.store.Get(ns, name)
	d.mu.Unlock()
	if !ok {
		return nil, ipc.NewError(ipc.CodeAppNotFound, fmt.Sprintf("app %q not found in namespace %q", name, ns))
	}
	return app, nil
}

func (d *Daemon) appStop(_ context.Context, raw json.RawMessage) (any, error) {
	var p ipc.StopParams
	if err := ipc.DecodeParams(raw, &p); err != nil {
		return nil, err
	}
	app, err := d.lookupApp(p.Namespace, p.Name)
	if err != nil {
		return nil, err
	}
	timeout := app.Spec.ShutdownTimeout
	if p.Timeout != "" {
		t, perr := time.ParseDuration(p.Timeout)
		if perr != nil || t <= 0 {
			return nil, ipc.ErrParams("invalid timeout: " + p.Timeout)
		}
		timeout = t
	}
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	d.stopApp(app, timeout, p.Now)
	d.mu.Lock()
	view := app.Snapshot()
	d.mu.Unlock()
	return map[string]any{"app": view}, nil
}

func (d *Daemon) appRestart(_ context.Context, raw json.RawMessage) (any, error) {
	var p ipc.RestartParams
	if err := ipc.DecodeParams(raw, &p); err != nil {
		return nil, err
	}
	app, err := d.lookupApp(p.Namespace, p.Name)
	if err != nil {
		return nil, err
	}
	timeout := 60 * time.Second
	if p.Timeout != "" {
		t, perr := time.ParseDuration(p.Timeout)
		if perr != nil || t <= 0 {
			return nil, ipc.ErrParams("invalid timeout: " + p.Timeout)
		}
		timeout = t
	}
	d.stopApp(app, app.Spec.ShutdownTimeout, false)
	d.mu.Lock()
	app.Restarts++
	app.Status = state.StatusStarting
	app.LastExit = nil
	_ = d.store.Save(app.Spec.Namespace)
	d.mu.Unlock()
	d.refreshApps()
	if err := d.launch(app, true, timeout); err != nil {
		d.mu.Lock()
		if app.Status == state.StatusStarting {
			app.Status = state.StatusError
			_ = d.store.Save(app.Spec.Namespace)
		}
		d.mu.Unlock()
		d.publish(EventAppStatus, app.Spec.Namespace, app.Spec.Name, app)
		d.refreshApps()
		return nil, err
	}
	d.mu.Lock()
	view := app.Snapshot()
	d.mu.Unlock()
	return map[string]any{"app": view}, nil
}

func (d *Daemon) appList(_ context.Context, raw json.RawMessage) (any, error) {
	var p ipc.ListParams
	if len(raw) > 0 {
		if err := ipc.DecodeParams(raw, &p); err != nil {
			return nil, err
		}
	}
	if !p.All {
		if p.Namespace == "" {
			return nil, ipc.ErrParams("namespace is required (or all=true)")
		}
		if !state.ValidName(p.Namespace) {
			return nil, ipc.ErrParams(fmt.Sprintf("invalid namespace %q", p.Namespace))
		}
	}
	d.mu.Lock()
	views := []*state.App{}
	for _, a := range d.store.List(p.Namespace, p.All) {
		views = append(views, a.Snapshot())
	}
	d.mu.Unlock()
	return map[string]any{"apps": views}, nil
}

func (d *Daemon) appStatus(_ context.Context, raw json.RawMessage) (any, error) {
	var p ipc.NameParams
	if err := ipc.DecodeParams(raw, &p); err != nil {
		return nil, err
	}
	app, err := d.lookupApp(p.Namespace, p.Name)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	view := app.Snapshot()
	d.mu.Unlock()
	return map[string]any{"app": view}, nil
}

// appHealth passes through the raw actuator health JSON (§10.1).
func (d *Daemon) appHealth(ctx context.Context, raw json.RawMessage) (any, error) {
	var p ipc.NameParams
	if err := ipc.DecodeParams(raw, &p); err != nil {
		return nil, err
	}
	app, err := d.lookupApp(p.Namespace, p.Name)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	active := app.Active()
	port := app.ActualPort
	healthPath := app.Spec.HealthPath
	d.mu.Unlock()
	if !active || port == 0 {
		return nil, ipc.NewError(ipc.CodeInvalidState, fmt.Sprintf("app %q is not running", p.Name))
	}
	hctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	body, _, herr := health.Get(hctx, port, healthPath)
	cancel()
	if herr != nil {
		return nil, ipc.NewError(ipc.CodeInvalidState,
			fmt.Sprintf("health check failed for %q: %v", p.Name, herr))
	}
	var v any
	if jerr := json.Unmarshal(body, &v); jerr != nil {
		return map[string]any{"raw": string(body)}, nil
	}
	return v, nil
}

func (d *Daemon) appDelete(_ context.Context, raw json.RawMessage) (any, error) {
	var p ipc.NameParams
	if err := ipc.DecodeParams(raw, &p); err != nil {
		return nil, err
	}
	app, err := d.lookupApp(p.Namespace, p.Name)
	if err != nil {
		return nil, err
	}
	if app.Active() {
		return nil, ipc.NewError(ipc.CodeInvalidState,
			fmt.Sprintf("app %q is %s; stop it first", p.Name, app.Status))
	}
	d.mu.Lock()
	derr := d.store.Delete(p.Namespace, p.Name)
	d.mu.Unlock()
	if derr != nil {
		return nil, ipc.NewError(ipc.CodeInternal, "persist state: "+derr.Error())
	}
	d.publish(EventAppDeleted, p.Namespace, p.Name, nil)
	d.refreshApps()
	return map[string]any{"status": "deleted"}, nil
}

func (d *Daemon) logsGet(_ context.Context, raw json.RawMessage) (any, error) {
	var p ipc.LogsParams
	if err := ipc.DecodeParams(raw, &p); err != nil {
		return nil, err
	}
	app, err := d.lookupApp(p.Namespace, p.Name)
	if err != nil {
		return nil, err
	}
	if app.LogPath == "" {
		return nil, ipc.NewError(ipc.CodeInvalidState, "app has no log file")
	}
	lines, lerr := proc.TailLines(app.LogPath, p.Lines)
	if lerr != nil {
		return nil, ipc.NewError(ipc.CodeInternal, "read log: "+lerr.Error())
	}
	return map[string]any{"lines": lines}, nil
}

func (d *Daemon) logsFollow(_ context.Context, raw json.RawMessage) (any, error) {
	var p ipc.LogsParams
	if err := ipc.DecodeParams(raw, &p); err != nil {
		return nil, err
	}
	app, err := d.lookupApp(p.Namespace, p.Name)
	if err != nil {
		return nil, err
	}
	path := app.LogPath
	if path == "" {
		return nil, ipc.NewError(ipc.CodeInvalidState, "app has no log file")
	}
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan ipc.StreamEvent, 16)
	go d.tailFollow(ctx, path, p.Lines, ch)
	return &ipc.Stream{Method: "logs.chunk", Events: ch, Cancel: cancel}, nil
}

func (d *Daemon) tailFollow(ctx context.Context, path string, lines int, ch chan<- ipc.StreamEvent) {
	defer close(ch)
	if lines > 0 {
		if tail, err := proc.TailLines(path, lines); err == nil && len(tail) > 0 {
			data := strings.Join(tail, "\n") + "\n"
			select {
			case ch <- ipc.StreamEvent{Params: map[string]any{"data": data}}:
			case <-ctx.Done():
				return
			}
		}
	}
	var f *os.File
	for {
		var err error
		f, err = os.Open(path)
		if err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(300 * time.Millisecond):
		}
	}
	defer f.Close()
	offset, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		st, err := f.Stat()
		if err != nil {
			return
		}
		if st.Size() < offset {
			offset = 0
		}
		if st.Size() > offset {
			buf := make([]byte, st.Size()-offset)
			n, rerr := f.ReadAt(buf, offset)
			offset += int64(n)
			if n > 0 {
				select {
				case ch <- ipc.StreamEvent{Params: map[string]any{"data": string(buf[:n])}}:
				case <-ctx.Done():
					return
				}
			}
			if rerr != nil && rerr != io.EOF {
				return
			}
			continue
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func (d *Daemon) eventsSubscribe(_ context.Context, raw json.RawMessage) (any, error) {
	var p ipc.SubscribeParams
	if len(raw) > 0 {
		if err := ipc.DecodeParams(raw, &p); err != nil {
			return nil, err
		}
	}
	events, unsub := d.bus.Subscribe(p.Namespace)
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan ipc.StreamEvent, 32)
	go func() {
		defer close(ch)
		defer unsub()
		for {
			select {
			case ev, ok := <-events:
				if !ok {
					return
				}
				params := map[string]any{"type": ev.Type, "namespace": ev.Namespace, "name": ev.Name}
				if ev.App != nil {
					params["app"] = ev.App
				}
				select {
				case ch <- ipc.StreamEvent{Method: "event", Params: params}:
				case <-ctx.Done():
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	return &ipc.Stream{Method: "event", Events: ch, Cancel: cancel}, nil
}
