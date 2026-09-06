package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"time"

	"spm4a/internal/health"
	"spm4a/internal/ipc"
	"spm4a/internal/proc"
	"spm4a/internal/state"
)

// Version 由 build.sh 经 -ldflags "-X spm4a/internal/daemon.Version=..." 注入；
// 裸 go build 时保持该默认值。
var Version = "0.1.0"

type Daemon struct {
	home       string
	ep         ipc.Endpoint
	startedAt  time.Time
	store      *state.Store
	bus        *Bus
	srv        *ipc.Server
	idle       *idleTracker
	pool       *proc.PortPool
	log        *slog.Logger
	shutdownCh chan struct{}

	mu           sync.Mutex
	procs        map[string]*proc.Proc
	probes       map[string]context.CancelFunc
	shutdownOnce sync.Once
}

func (d *Daemon) key(ns, name string) string { return ns + "/" + name }

func Run(home string) error {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ep := ipc.Endpoint{Home: home}
	for _, dir := range []string{ep.RunDir(), filepath.Join(home, "namespaces")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	ln, err := ep.Listen()
	if err != nil {
		return fmt.Errorf("listen %s: %w (another daemon running?)", ep.Address(), err)
	}
	store, err := state.Load(filepath.Join(home, "namespaces"))
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}
	d := &Daemon{
		home:       home,
		ep:         ep,
		startedAt:  time.Now(),
		store:      store,
		bus:        NewBus(),
		pool:       proc.NewPortPool(10000, 60000),
		log:        log,
		shutdownCh: make(chan struct{}),
		procs:      map[string]*proc.Proc{},
		probes:     map[string]context.CancelFunc{},
	}
	d.idle = newIdleTracker(d.initiateShutdown)
	d.adopt()

	d.srv = ipc.NewServer()
	d.srv.SetMiddleware(func(h ipc.Handler) ipc.Handler {
		return func(ctx context.Context, params json.RawMessage) (any, error) {
			d.idle.enter()
			defer d.idle.leave()
			return h(ctx, params)
		}
	})
	d.register()

	info := map[string]any{"pid": os.Getpid(), "version": Version, "startedAt": d.startedAt.Format(time.RFC3339)}
	if data, err := json.Marshal(info); err == nil {
		_ = os.WriteFile(ep.DaemonInfoPath(), data, 0o644)
	}

	serveDone := make(chan struct{})
	go func() {
		defer close(serveDone)
		if err := d.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http serve", "err", err)
			d.initiateShutdown()
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	log.Info("daemon ready", "pid", os.Getpid(), "addr", ep.Address(), "version", Version)
	select {
	case <-d.shutdownCh:
	case <-sigCh:
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	_ = d.srv.Shutdown(ctx)
	cancel()
	_ = ln.Close()
	<-serveDone
	_ = os.Remove(ep.DaemonInfoPath())
	ep.Cleanup()
	log.Info("daemon stopped")
	return nil
}

// initiateShutdown triggers the Run loop to exit (idempotent).
func (d *Daemon) initiateShutdown() {
	d.shutdownOnce.Do(func() { close(d.shutdownCh) })
}

// requestShutdown schedules a shutdown after the grace delay so the
// triggering response can flush.
func (d *Daemon) requestShutdown() {
	d.idle.forceShutdown()
}

// adopt re-attaches to apps from state.json after a daemon restart: probe
// each PID; alive + health UP becomes ready, alive-only unready, dead
// stopped. Liveness probes are re-attached for adopted apps.
func (d *Daemon) adopt() {
	d.mu.Lock()
	changed := map[string]bool{}
	var adoptKeys []string
	for _, app := range d.store.List("", true) {
		if !app.Active() {
			app.PID = 0
			continue
		}
		healthPath := app.Spec.HealthPath
		if healthPath == "" {
			healthPath = "/actuator/health"
			app.Spec.HealthPath = healthPath
		}
		switch {
		case app.PID == 0 || !proc.Alive(app.PID):
			app.Status = state.StatusStopped
			app.PID = 0
		case app.ActualPort > 0 && healthUp(app.ActualPort, healthPath):
			app.Status = state.StatusReady
		default:
			app.Status = state.StatusUnready
		}
		if app.Active() {
			adoptKeys = append(adoptKeys, d.key(app.Spec.Namespace, app.Spec.Name))
		}
		changed[app.Spec.Namespace] = true
	}
	for ns := range changed {
		if err := d.store.Save(ns); err != nil {
			d.log.Warn("persist adopted state", "ns", ns, "err", err)
		}
	}
	n := d.store.ActiveCount()
	d.mu.Unlock()
	for _, key := range adoptKeys {
		d.startProbe(key)
	}
	d.idle.setApps(n)
	if n > 0 {
		d.log.Info("adopted apps", "active", n)
	}
}

func healthUp(port int, path string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return health.IsUp(ctx, port, path)
}

func (d *Daemon) publish(evType, ns, name string, app *state.App) {
	ev := Event{Type: evType, Namespace: ns, Name: name}
	if app != nil {
		ev.App = app.Snapshot()
	}
	d.bus.Publish(ev)
}

func (d *Daemon) refreshApps() {
	d.mu.Lock()
	n := d.store.ActiveCount()
	d.mu.Unlock()
	d.idle.setApps(n)
}
