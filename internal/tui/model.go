package tui

import (
	"context"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"spm4a/internal/state"
)

type model struct {
	client Client
	ns     string
	all    bool

	apps   []*state.App
	cursor int
	selKey string // selection identity, survives list refreshes

	met     *metricsCache
	metrics map[int32]appMetrics

	logLines  []string
	logKey    string // app whose logs are loaded
	logGen    int
	logChan   <-chan string
	logCancel func()

	eventsGen    int
	eventsCh     <-chan AppEvent
	eventsCancel func()

	viewport  viewport.Model
	focusLogs bool
	confirm   *state.App

	statusMsg string
	statusErr bool

	width, height int
	initialized   bool
}

type appsMsg []*state.App
type metricsMsg map[int32]appMetrics
type logsReadyMsg struct {
	gen    int
	ch     <-chan string
	cancel func()
	err    error
}
type logChunkMsg struct {
	gen   int
	chunk string
}
type logEndMsg struct{ gen int }
type eventsReadyMsg struct {
	gen    int
	ch     <-chan AppEvent
	cancel func()
	err    error
}
type eventMsg struct {
	gen int
	ev  AppEvent
}
type eventEndMsg struct{ gen int }
type actionMsg struct {
	text string
	err  bool
}
type tickMsg time.Time

// NewModel builds the TUI model around an injectable Client.
func NewModel(client Client, ns string, all bool) tea.Model {
	return &model{client: client, ns: ns, all: all, met: newMetricsCache()}
}

// Run starts the full-screen TUI.
func Run(ctx context.Context, client Client, ns string, all bool) error {
	p := tea.NewProgram(NewModel(client, ns, all), tea.WithAltScreen(), tea.WithContext(ctx))
	_, err := p.Run()
	return err
}

func (m *model) Init() tea.Cmd {
	return tea.Batch(m.fetchApps, m.subscribeEventsCmd(), tickCmd())
}

func keyOf(ns, name string) string { return ns + "/" + name }

func (m *model) selected() *state.App {
	if m.cursor < 0 || m.cursor >= len(m.apps) {
		return nil
	}
	return m.apps[m.cursor]
}

func (m *model) fetchApps() tea.Msg {
	apps, err := m.client.ListApps(context.Background(), m.ns, m.all)
	if err != nil {
		return actionMsg{text: "list: " + err.Error(), err: true}
	}
	return appsMsg(apps)
}

func tickCmd() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m *model) subscribeEventsCmd() tea.Cmd {
	m.eventsGen++
	gen := m.eventsGen
	ns := m.ns
	if m.all {
		ns = ""
	}
	return func() tea.Msg {
		ch, cancel, err := m.client.Events(context.Background(), ns)
		return eventsReadyMsg{gen: gen, ch: ch, cancel: cancel, err: err}
	}
}

func waitEventCmd(gen int, ch <-chan AppEvent) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return eventEndMsg{gen}
		}
		return eventMsg{gen, ev}
	}
}

func (m *model) followLogsCmd(app *state.App) tea.Cmd {
	m.logGen++
	gen := m.logGen
	ns, name := app.Spec.Namespace, app.Spec.Name
	return func() tea.Msg {
		ch, cancel, err := m.client.FollowLogs(context.Background(), ns, name, 200)
		return logsReadyMsg{gen: gen, ch: ch, cancel: cancel, err: err}
	}
}

func waitLogCmd(gen int, ch <-chan string) tea.Cmd {
	return func() tea.Msg {
		s, ok := <-ch
		if !ok {
			return logEndMsg{gen}
		}
		return logChunkMsg{gen, s}
	}
}

func (m *model) fetchMetrics() tea.Msg {
	var pids []int32
	for _, a := range m.apps {
		if a.PID != 0 && a.Active() {
			pids = append(pids, int32(a.PID))
		}
	}
	return metricsMsg(m.met.measureAll(pids))
}

func (m *model) actionCmd(f func() (string, error)) tea.Cmd {
	return func() tea.Msg {
		text, err := f()
		if err != nil {
			return actionMsg{text: err.Error(), err: true}
		}
		return actionMsg{text: text}
	}
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.initialized = true
		m.viewport = viewport.New(msg.Width, m.logPanelHeight())
		m.viewport.SetContent(strings.Join(m.logLines, "\n"))
		return m, nil

	case appsMsg:
		m.apps = msg
		m.fixSelection()
		m.viewport.Height = m.logPanelHeight()
		return m, m.syncLogsCmd()

	case metricsMsg:
		m.metrics = msg
		return m, nil

	case tickMsg:
		return m, tea.Batch(m.fetchApps, m.fetchMetrics, tickCmd())

	case eventsReadyMsg:
		if msg.gen != m.eventsGen {
			if msg.cancel != nil {
				msg.cancel()
			}
			return m, nil
		}
		if msg.err != nil {
			m.statusMsg, m.statusErr = "events: "+msg.err.Error(), true
			return m, nil
		}
		m.eventsCh, m.eventsCancel = msg.ch, msg.cancel
		return m, waitEventCmd(msg.gen, msg.ch)

	case eventMsg:
		if msg.gen != m.eventsGen {
			return m, nil
		}
		m.applyEvent(msg.ev)
		return m, tea.Batch(waitEventCmd(msg.gen, m.eventsCh), m.syncLogsCmd())

	case eventEndMsg:
		return m, nil

	case logsReadyMsg:
		if msg.gen != m.logGen {
			if msg.cancel != nil {
				msg.cancel()
			}
			return m, nil
		}
		if msg.err != nil {
			m.logLines = []string{"(logs unavailable: " + msg.err.Error() + ")"}
			m.refreshViewport()
			return m, nil
		}
		m.logChan, m.logCancel = msg.ch, msg.cancel
		return m, waitLogCmd(msg.gen, msg.ch)

	case logChunkMsg:
		if msg.gen != m.logGen {
			return m, nil
		}
		m.appendLog(msg.chunk)
		return m, waitLogCmd(msg.gen, m.logChan)

	case logEndMsg:
		return m, nil

	case actionMsg:
		m.statusMsg, m.statusErr = msg.text, msg.err
		return m, m.fetchApps

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m *model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.confirm != nil {
		switch msg.String() {
		case "y", "Y":
			app := m.confirm
			m.confirm = nil
			return m, m.actionCmd(func() (string, error) {
				err := m.client.DeleteApp(context.Background(), app.Spec.Namespace, app.Spec.Name)
				if err != nil {
					return "", err
				}
				return "removed " + app.Spec.Name, nil
			})
		default:
			m.confirm = nil
			m.statusMsg, m.statusErr = "delete cancelled", false
			return m, nil
		}
	}

	if m.focusLogs {
		switch msg.String() {
		case "esc":
			m.focusLogs = false
			return m, nil
		case "q", "ctrl+c":
			m.cleanup()
			return m, tea.Quit
		}
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}

	switch msg.String() {
	case "q", "ctrl+c":
		m.cleanup()
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
			m.selKey = ""
			return m, m.syncLogsCmd()
		}
	case "down", "j":
		if m.cursor < len(m.apps)-1 {
			m.cursor++
			m.selKey = ""
			return m, m.syncLogsCmd()
		}
	case "A":
		m.all = !m.all
		m.selKey = ""
		m.cursor = 0
		if m.eventsCancel != nil {
			m.eventsCancel()
		}
		return m, tea.Batch(m.fetchApps, m.subscribeEventsCmd())
	case "enter":
		if m.selected() != nil {
			m.focusLogs = true
		}
	case "s":
		if app := m.selected(); app != nil {
			return m, m.actionCmd(func() (string, error) {
				err := m.client.StopApp(context.Background(), app.Spec.Namespace, app.Spec.Name)
				if err != nil {
					return "", err
				}
				return "stopped " + app.Spec.Name, nil
			})
		}
	case "r":
		if app := m.selected(); app != nil {
			return m, m.actionCmd(func() (string, error) {
				err := m.client.RestartApp(context.Background(), app.Spec.Namespace, app.Spec.Name)
				if err != nil {
					return "", err
				}
				return "restarted " + app.Spec.Name, nil
			})
		}
	case "l":
		if app := m.selected(); app != nil {
			return m, m.actionCmd(func() (string, error) {
				summary, err := m.client.ReloadApp(context.Background(), app.Spec.Namespace, app.Spec.Name)
				if err != nil {
					return "", err
				}
				return "reload " + app.Spec.Name + ": " + summary, nil
			})
		}
	case "d":
		if app := m.selected(); app != nil {
			if app.Status != state.StatusStopped && app.Status != state.StatusError {
				m.statusMsg, m.statusErr = app.Spec.Name+" is "+app.Status+"; stop it first", true
				return m, nil
			}
			m.confirm = app
			return m, nil
		}
	}
	return m, nil
}

func (m *model) cleanup() {
	if m.eventsCancel != nil {
		m.eventsCancel()
	}
	if m.logCancel != nil {
		m.logCancel()
	}
}

// fixSelection keeps the cursor on the same app across list refreshes.
func (m *model) fixSelection() {
	if m.cursor >= len(m.apps) {
		m.cursor = len(m.apps) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.selKey == "" {
		if app := m.selected(); app != nil {
			m.selKey = keyOf(app.Spec.Namespace, app.Spec.Name)
		}
		return
	}
	for i, a := range m.apps {
		if keyOf(a.Spec.Namespace, a.Spec.Name) == m.selKey {
			m.cursor = i
			return
		}
	}
}

// syncLogsCmd follows the selected app's logs when the selection changed.
func (m *model) syncLogsCmd() tea.Cmd {
	app := m.selected()
	var key string
	if app != nil {
		key = keyOf(app.Spec.Namespace, app.Spec.Name)
	}
	if key == m.logKey {
		return nil
	}
	m.logKey = key
	if m.logCancel != nil {
		m.logCancel()
		m.logCancel = nil
		m.logChan = nil
	}
	m.logLines = nil
	m.refreshViewport()
	if app == nil {
		return nil
	}
	return m.followLogsCmd(app)
}

func (m *model) applyEvent(ev AppEvent) {
	if !m.all && ev.Namespace != m.ns {
		return
	}
	k := keyOf(ev.Namespace, ev.Name)
	if ev.Type == "app.deleted" {
		for i, a := range m.apps {
			if keyOf(a.Spec.Namespace, a.Spec.Name) == k {
				m.apps = append(m.apps[:i], m.apps[i+1:]...)
				break
			}
		}
		m.fixSelection()
		m.viewport.Height = m.logPanelHeight()
		return
	}
	if ev.App == nil {
		return
	}
	for i, a := range m.apps {
		if keyOf(a.Spec.Namespace, a.Spec.Name) == k {
			m.apps[i] = ev.App
			return
		}
	}
	m.apps = append(m.apps, ev.App)
	m.fixSelection()
	m.viewport.Height = m.logPanelHeight()
}

func (m *model) appendLog(chunk string) {
	lines := strings.Split(strings.TrimRight(chunk, "\n"), "\n")
	m.logLines = append(m.logLines, lines...)
	const max = 2000
	if len(m.logLines) > max {
		m.logLines = m.logLines[len(m.logLines)-max:]
	}
	m.refreshViewport()
}

func (m *model) refreshViewport() {
	if !m.initialized {
		return
	}
	m.viewport.SetContent(strings.Join(m.logLines, "\n"))
	m.viewport.GotoBottom()
}

// logPanelHeight: the table takes what it needs (capped), logs get the rest.
func (m *model) logPanelHeight() int {
	table := m.tableHeight()
	h := m.height - table - 2 // status bar + separator
	if h < 3 {
		h = 3
	}
	return h
}

func (m *model) tableHeight() int {
	rows := len(m.apps) + 1 // header
	if m.all {
		groups := map[string]bool{}
		for _, a := range m.apps {
			groups[a.Spec.Namespace] = true
		}
		rows += len(groups)
	}
	maxH := m.height/2 - 1
	if maxH < 4 {
		maxH = 4
	}
	if rows > maxH {
		rows = maxH
	}
	return rows
}
