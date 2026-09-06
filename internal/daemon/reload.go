package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"spm4a/internal/health"
	"spm4a/internal/ipc"
	"spm4a/internal/launcher"
	"spm4a/internal/proc"
)

// appReload implements §11: run the build tool's compile step in the workdir
// (maven: mvn compile; gradle: gradle classes), which triggers a devtools
// hot restart. Blocks until the build finishes; on success waits for the app
// to come back ready.
func (d *Daemon) appReload(ctx context.Context, raw json.RawMessage) (any, error) {
	var p ipc.ReloadParams
	if err := ipc.DecodeParams(raw, &p); err != nil {
		return nil, err
	}
	app, err := d.lookupApp(p.Namespace, p.Name)
	if err != nil {
		return nil, err
	}
	timeout := 120 * time.Second
	if p.Timeout != "" {
		t, perr := time.ParseDuration(p.Timeout)
		if perr != nil || t <= 0 {
			return nil, ipc.ErrParams("invalid timeout: " + p.Timeout)
		}
		timeout = t
	}

	d.mu.Lock()
	spec := app.Spec
	active := app.Active()
	port := app.ActualPort
	d.mu.Unlock()
	if !active {
		return nil, ipc.NewError(ipc.CodeInvalidState, fmt.Sprintf("app %q is not running", p.Name))
	}
	var cmdArgv []string
	switch spec.Launcher {
	case "maven":
		mvn, rerr := launcher.ResolveMaven()
		if rerr != nil {
			return nil, ipc.NewError(ipc.CodeInvalidParams, rerr.Error())
		}
		cmdArgv = append([]string{mvn}, "compile")
	case "gradle":
		g, rerr := launcher.ResolveGradle(spec.Workdir)
		if rerr != nil {
			return nil, ipc.NewError(ipc.CodeInvalidParams, rerr.Error())
		}
		cmdArgv = append([]string{g}, "classes")
	default:
		return nil, ipc.NewError(ipc.CodeInvalidState,
			fmt.Sprintf("reload is not supported for launcher %q (needs maven/gradle with devtools)", spec.Launcher))
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	deadline := time.Now().Add(timeout)

	buf := &tailBuffer{}
	cmd := exec.Command(cmdArgv[0], cmdArgv[1:]...)
	cmd.Dir = spec.Workdir
	env := map[string]string{}
	for _, e := range os.Environ() {
		if k, v, ok := strings.Cut(e, "="); ok {
			env[k] = v
		}
	}
	for k, v := range spec.Env {
		env[k] = v
	}
	if spec.JDK != "" {
		env["JAVA_HOME"] = spec.JDK
	}
	cmd.Env = envList(env)
	cmd.Stdout = buf
	cmd.Stderr = buf
	rp, serr := proc.Start(cmd)
	if serr != nil {
		return nil, ipc.NewError(ipc.CodeInternal, "run build: "+serr.Error())
	}
	waitCh := make(chan error, 1)
	go func() { waitCh <- rp.Err() }()
	var waitErr error
	select {
	case waitErr = <-waitCh:
	case <-ctx.Done():
		_ = rp.Kill()
		<-waitCh
		return nil, ipc.NewError(ipc.CodeReadyTimeout, fmt.Sprintf("build for %q timed out after %s", p.Name, timeout))
	}

	res := &ipc.ReloadResult{ExitCode: proc.ExitCode(waitErr), Output: buf.String()}
	if res.ExitCode != 0 {
		res.AppStatus = d.statusOf(p.Namespace, p.Name)
		return res, nil
	}

	// Compilation succeeded; devtools may hot-restart the context. Wait until
	// the app is healthy again (it may already be, if nothing recompiled).
	if port > 0 {
		for time.Now().Before(deadline) {
			hctx, hcancel := context.WithTimeout(context.Background(), 2*time.Second)
			up := health.IsUp(hctx, port, spec.HealthPath)
			hcancel()
			if up {
				hctx, hcancel = context.WithTimeout(context.Background(), 2*time.Second)
				body, _, herr := health.Get(hctx, port, spec.HealthPath)
				hcancel()
				if herr == nil {
					var v any
					if json.Unmarshal(body, &v) == nil {
						res.Health = v
					}
				}
				break
			}
			select {
			case <-ctx.Done():
			case <-time.After(500 * time.Millisecond):
			}
		}
	}
	res.AppStatus = d.statusOf(p.Namespace, p.Name)
	return res, nil
}

func (d *Daemon) statusOf(ns, name string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if app, ok := d.store.Get(ns, name); ok {
		return app.Status
	}
	return ""
}

// tailBuffer captures process output, keeping everything but returning only
// the tail on String().
type tailBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.buf.String()
	const max = 8 << 10
	if len(s) > max {
		s = s[len(s)-max:]
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		}
	}
	return s
}
