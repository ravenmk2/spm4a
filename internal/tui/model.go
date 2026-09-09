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

	apps       []*state.App
	cursor     int
	selKey     string // selection identity, survives list refreshes
	listOffset int    // first visible table line (scrolling)

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

	viewport viewport.Model // logs (right panel)
	detailVP viewport.Model // selected-app detail (bottom-left panel)
	focus    int
	confirm  *state.App

	statusMsg string
	statusErr bool

	daemonVersion string

	width, height int
	initialized   bool
}

// focus targets: the app list (top-left), the detail panel (bottom-left) and
// the logs panel (right).
const (
	focusList = iota
	focusDetail
	focusLogs
)

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
type daemonInfoMsg struct{ version string }
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
	return tea.Batch(m.fetchDaemonInfo, m.fetchApps, m.subscribeEventsCmd(), tickCmd())
}

func (m *model) fetchDaemonInfo() tea.Msg {
	v, err := m.client.DaemonInfo(context.Background())
	if err != nil {
		return daemonInfoMsg{""}
	}
	return daemonInfoMsg{v}
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
		m.viewport = viewport.New(m.width-m.leftWidth()-2, m.viewportHeight())
		m.detailVP = viewport.New(m.leftWidth()-2, m.detailHeight())
		m.refreshViewport()
		m.refreshDetail()
		m.adjustOffset()
		return m, nil

	case appsMsg:
		m.apps = msg
		m.fixSelection()
		m.adjustOffset()
		m.syncPanelSizes()
		m.refreshDetail()
		return m, m.syncLogsCmd()

	case metricsMsg:
		m.metrics = msg
		m.refreshDetail()
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
		m.adjustOffset()
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

	case daemonInfoMsg:
		m.daemonVersion = msg.version
		return m, nil

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

	if m.focus != focusList {
		switch msg.String() {
		case "esc":
			m.focus = focusList
			return m, nil
		case "tab":
			m.focus = (m.focus + 1) % 3
			return m, nil
		case "shift+tab":
			m.focus = (m.focus + 2) % 3
			return m, nil
		case "q", "ctrl+c":
			m.cleanup()
			return m, tea.Quit
		}
		var cmd tea.Cmd
		if m.focus == focusDetail {
			m.detailVP, cmd = m.detailVP.Update(msg)
		} else {
			m.viewport, cmd = m.viewport.Update(msg)
		}
		return m, cmd
	}

	switch msg.String() {
	case "q", "ctrl+c":
		m.cleanup()
		return m, tea.Quit
	case "tab":
		if m.selected() != nil {
			m.focus = focusDetail
		}
		return m, nil
	case "shift+tab":
		m.focus = focusLogs
		return m, nil
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
			m.selKey = ""
			m.adjustOffset()
			m.refreshDetail()
			return m, m.syncLogsCmd()
		}
	case "down", "j":
		if m.cursor < len(m.apps)-1 {
			m.cursor++
			m.selKey = ""
			m.adjustOffset()
			m.refreshDetail()
			return m, m.syncLogsCmd()
		}
	case "A":
		m.all = !m.all
		m.selKey = ""
		m.cursor = 0
		m.listOffset = 0
		if m.eventsCancel != nil {
			m.eventsCancel()
		}
		return m, tea.Batch(m.fetchApps, m.subscribeEventsCmd())
	case "enter":
		if m.selected() != nil {
			m.focus = focusLogs
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

// adjustOffset keeps the selected app inside the visible table window.
func (m *model) adjustOffset() {
	visible := m.appsRows() - 1 // minus the column header
	if visible < 1 {
		visible = 1
	}
	selLine := m.cursor
	lines := len(m.apps)
	if m.listOffset > selLine {
		m.listOffset = selLine
	}
	if selLine >= m.listOffset+visible {
		m.listOffset = selLine - visible + 1
	}
	if maxOffset := lines - visible; maxOffset >= 0 && m.listOffset > maxOffset {
		m.listOffset = maxOffset
	}
	if m.listOffset < 0 {
		m.listOffset = 0
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
		m.syncPanelSizes()
		m.refreshDetail()
		return
	}
	if ev.App == nil {
		return
	}
	for i, a := range m.apps {
		if keyOf(a.Spec.Namespace, a.Spec.Name) == k {
			m.apps[i] = ev.App
			m.refreshDetail()
			return
		}
	}
	m.apps = append(m.apps, ev.App)
	m.fixSelection()
	m.syncPanelSizes()
	m.refreshDetail()
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

func (m *model) refreshDetail() {
	if !m.initialized {
		return
	}
	m.detailVP.SetContent(m.renderDetail())
}

// syncPanelSizes reapplies panel dimensions after the app count changes
// (the Apps panel grows, the Detail panel shrinks).
func (m *model) syncPanelSizes() {
	if !m.initialized {
		return
	}
	m.viewport.Width = m.width - m.leftWidth() - 2
	m.viewport.Height = m.viewportHeight()
	m.detailVP.Width = m.leftWidth() - 2
	m.detailVP.Height = m.detailHeight()
}

// mainHeight: rows between the header and the status bar.
func (m *model) mainHeight() int { return m.height - 2 }

// tooSmall: below this size the two-column layout is unusable.
func (m *model) tooSmall() bool { return m.width < 60 || m.height < 10 }

// leftWidth: the narrow left column (Apps + Detail panels).
func (m *model) leftWidth() int {
	w := m.width / 3
	if w > 36 {
		w = 36
	}
	if w < 24 {
		w = 24
	}
	if max := m.width - 30; w > max {
		w = max
	}
	return w
}

// appsRows: content rows of the Apps panel, including the column header.
// The Detail panel keeps at least 4 content rows.
func (m *model) appsRows() int {
	total := len(m.apps) + 1
	if total < 2 {
		total = 2 // header + "(no apps)" line
	}
	cap := m.mainHeight() - 8 // apps borders(2) + detail borders(2) + detail min(4)
	if cap < 2 {
		cap = 2
	}
	if total > cap {
		total = cap
	}
	return total
}

// detailHeight: content rows of the Detail panel — the rest of the left column.
func (m *model) detailHeight() int {
	h := m.mainHeight() - m.appsRows() - 4
	if h < 1 {
		return 1
	}
	return h
}

// viewportHeight: content rows of the Logs viewport (full-height right panel).
func (m *model) viewportHeight() int {
	if h := m.mainHeight() - 2; h > 0 {
		return h
	}
	return 1
}
