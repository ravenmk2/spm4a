package tui_test

import (
	"bytes"
	"context"
	"io"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"

	"spm4a/internal/state"
	"spm4a/internal/tui"
)

type fakeClient struct {
	mu       sync.Mutex
	apps     []*state.App
	logs     map[string]string
	followCh map[string]chan string
	eventsCh chan tui.AppEvent

	stops    []string
	restarts []string
	reloads  []string
	deletes  []string
}

func newFakeClient() *fakeClient {
	return &fakeClient{
		logs:     map[string]string{},
		followCh: map[string]chan string{},
		eventsCh: make(chan tui.AppEvent, 16),
	}
}

func fakeApp(ns, name, status string) *state.App {
	return &state.App{
		Spec:   state.Spec{Name: name, Namespace: ns, Workdir: "/x", Launcher: "jar"},
		Status: status,
	}
}

func (f *fakeClient) ListApps(context.Context, string, bool) ([]*state.App, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*state.App, len(f.apps))
	for i, a := range f.apps {
		out[i] = a.Snapshot()
	}
	return out, nil
}

func (f *fakeClient) StopApp(_ context.Context, ns, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stops = append(f.stops, ns+"/"+name)
	return nil
}

func (f *fakeClient) RestartApp(_ context.Context, ns, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restarts = append(f.restarts, ns+"/"+name)
	return nil
}

func (f *fakeClient) ReloadApp(_ context.Context, ns, name string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reloads = append(f.reloads, ns+"/"+name)
	return "build exit 0, health UP", nil
}

func (f *fakeClient) DeleteApp(_ context.Context, ns, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deletes = append(f.deletes, ns+"/"+name)
	return nil
}

func (f *fakeClient) FollowLogs(_ context.Context, ns, name string, _ int) (<-chan string, func(), error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch := make(chan string, 16)
	f.followCh[ns+"/"+name] = ch
	if s := f.logs[ns+"/"+name]; s != "" {
		ch <- s + "\n"
	}
	return ch, func() {}, nil
}

func (f *fakeClient) Events(context.Context, string) (<-chan tui.AppEvent, func(), error) {
	return f.eventsCh, func() {}, nil
}

func (f *fakeClient) calledStops() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.stops...)
}

// watcher accumulates the (consumable) virtual-terminal output stream so
// successive assertions see the full history.
// (A hand-rolled loop is used instead of teatest.WaitFor, which
// intermittently observed an empty stream on this Windows + ANSI-compressor
// setup.)
type watcher struct {
	tm  *teatest.TestModel
	acc bytes.Buffer
}

func newWatcher(tm *teatest.TestModel) *watcher { return &watcher{tm: tm} }

func (w *watcher) wait(t *testing.T, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		_, _ = io.Copy(&w.acc, io.LimitReader(w.tm.Output(), 1<<20))
		if bytes.Contains(w.acc.Bytes(), []byte(want)) {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	s := w.acc.String()
	if len(s) > 800 {
		s = s[:800]
	}
	t.Fatalf("output never contained %q; got:\n%s", want, s)
}

// newTestModel starts the model under a virtual terminal. The short settle
// delay works around a teatest startup race (ANSI-compressed first frames
// can be missed by an immediately-started WaitFor).
func newTestModel(t *testing.T, fc *fakeClient, ns string, all bool) *teatest.TestModel {
	t.Helper()
	tm := teatest.NewTestModel(t, tui.NewModel(fc, ns, all), teatest.WithInitialTermSize(120, 40))
	time.Sleep(200 * time.Millisecond)
	return tm
}

func sendKey(tm *teatest.TestModel, r rune) {
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
}

func quit(t *testing.T, tm *teatest.TestModel) {
	t.Helper()
	sendKey(tm, 'q')
	tm.WaitFinished(t, teatest.WithFinalTimeout(5*time.Second))
}

func TestTuiInitialRender(t *testing.T) {
	fc := newFakeClient()
	fc.apps = []*state.App{fakeApp("test", "demo-app", "ready")}
	fc.logs["test/demo-app"] = "first log line of demo-app"

	tm := newTestModel(t, fc, "test", false)
	w := newWatcher(tm)
	w.wait(t, "demo-app")
	w.wait(t, "ready")
	w.wait(t, "first log line of demo-app")
	quit(t, tm)
}

func TestTuiNavigationSwitchesLogs(t *testing.T) {
	fc := newFakeClient()
	fc.apps = []*state.App{
		fakeApp("test", "app-one", "ready"),
		fakeApp("test", "app-two", "stopped"),
	}
	fc.logs["test/app-one"] = "log-of-app-one"
	fc.logs["test/app-two"] = "log-of-app-two"

	tm := newTestModel(t, fc, "test", false)
	w := newWatcher(tm)
	w.wait(t, "log-of-app-one")
	sendKey(tm, 'j')
	w.wait(t, "log-of-app-two")
	quit(t, tm)
}

func TestTuiStopActionCallsClient(t *testing.T) {
	fc := newFakeClient()
	fc.apps = []*state.App{fakeApp("test", "demo-app", "ready")}

	tm := newTestModel(t, fc, "test", false)
	w := newWatcher(tm)
	w.wait(t, "demo-app")
	sendKey(tm, 's')
	w.wait(t, "stopped demo-app")
	stops := fc.calledStops()
	if len(stops) != 1 || stops[0] != "test/demo-app" {
		t.Fatalf("stops = %v, want [test/demo-app]", stops)
	}
	quit(t, tm)
}

func TestTuiEventDrivenUpdate(t *testing.T) {
	fc := newFakeClient()
	fc.apps = []*state.App{fakeApp("test", "demo-app", "ready")}

	tm := newTestModel(t, fc, "test", false)
	w := newWatcher(tm)
	w.wait(t, "demo-app")

	changed := fakeApp("test", "demo-app", "unready")
	fc.eventsCh <- tui.AppEvent{Type: "app.status", Namespace: "test", Name: "demo-app", App: changed}
	w.wait(t, "unready")
	quit(t, tm)
}

func TestTuiAllNamespacesToggle(t *testing.T) {
	fc := newFakeClient()
	fc.apps = []*state.App{fakeApp("test", "demo-app", "ready")}

	tm := newTestModel(t, fc, "test", false)
	w := newWatcher(tm)
	w.wait(t, "demo-app")
	// -A rendering adds a NAMESPACE column
	sendKey(tm, 'A')
	w.wait(t, "NAMESPACE")
	quit(t, tm)
}
