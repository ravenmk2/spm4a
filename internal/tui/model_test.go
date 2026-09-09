package tui_test

import (
	"bytes"
	"context"
	"fmt"
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

func (f *fakeClient) DaemonInfo(context.Context) (string, error) { return "0.1.0-fake", nil }

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
	// -A switches the Apps panel to the all-namespaces view (names as ns/name)
	sendKey(tm, 'A')
	w.wait(t, " Apps(all) ")
	w.wait(t, "test/demo-app")
	quit(t, tm)
}

func TestTuiListScrolls(t *testing.T) {
	fc := newFakeClient()
	for i := 0; i < 10; i++ {
		fc.apps = append(fc.apps, fakeApp("test", fmt.Sprintf("app-%02d", i), "ready"))
	}

	// 20-row terminal -> table window shows fewer than 10 apps; scrolling must
	// bring the last row into view when the cursor reaches it.
	tm := teatest.NewTestModel(t, tui.NewModel(fc, "test", false), teatest.WithInitialTermSize(120, 20))
	time.Sleep(200 * time.Millisecond)
	w := newWatcher(tm)
	w.wait(t, "app-00")
	for i := 0; i < 9; i++ {
		sendKey(tm, 'j')
	}
	// app-09 only enters the visible window after the cursor scrolls it in
	w.wait(t, "app-09")
	quit(t, tm)
}

func TestTuiBorderLayout(t *testing.T) {
	fc := newFakeClient()
	fc.apps = []*state.App{fakeApp("test", "demo-app", "ready")}

	tm := newTestModel(t, fc, "test", false)
	w := newWatcher(tm)
	// header bar: brand left, context right (ns filter, daemon version, count)
	w.wait(t, "SPM4A")
	w.wait(t, "ns: test")
	w.wait(t, "0.1.0-fake")
	w.wait(t, "apps: 1")
	// square panels with embedded titles
	w.wait(t, "┌")
	w.wait(t, "└")
	w.wait(t, " Apps(test) ")
	w.wait(t, " Logs: test/demo-app ")

	// focusing the logs panel marks its title
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	w.wait(t, " Logs: test/demo-app * ")
	// esc returns to the list
	tm.Send(tea.KeyMsg{Type: tea.KeyEscape})
	quit(t, tm)
}

func TestTuiDetailPanel(t *testing.T) {
	fc := newFakeClient()
	fc.apps = []*state.App{fakeApp("test", "demo-app", "ready")}

	tm := newTestModel(t, fc, "test", false)
	w := newWatcher(tm)
	// bottom-left panel shows the selected app's config/status properties
	w.wait(t, " Detail: test/demo-app ")
	w.wait(t, "workdir: /x")
	w.wait(t, "launcher: jar")
	// tab moves focus to the detail panel (title marked, border highlighted)
	tm.Send(tea.KeyMsg{Type: tea.KeyTab})
	w.wait(t, " Detail: test/demo-app * ")
	// tab again moves to logs, esc back to the list
	tm.Send(tea.KeyMsg{Type: tea.KeyTab})
	w.wait(t, " Logs: test/demo-app * ")
	tm.Send(tea.KeyMsg{Type: tea.KeyEscape})
	quit(t, tm)
}

func TestTuiLogsCollapsedOnTinyTerminal(t *testing.T) {
	fc := newFakeClient()
	fc.apps = []*state.App{fakeApp("test", "demo-app", "ready")}

	tm := teatest.NewTestModel(t, tui.NewModel(fc, "test", false), teatest.WithInitialTermSize(120, 8))
	time.Sleep(200 * time.Millisecond)
	w := newWatcher(tm)
	w.wait(t, "terminal too small")
	quit(t, tm)
}
