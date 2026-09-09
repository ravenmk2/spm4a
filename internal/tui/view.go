package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/reflow/truncate"

	"spm4a/internal/state"
)

var (
	styleHeader      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	styleSelected    = lipgloss.NewStyle().Reverse(true)
	styleStatusBar   = lipgloss.NewStyle().Foreground(lipgloss.Color("7"))
	styleErr         = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	styleDim         = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	styleReady       = lipgloss.NewStyle().Foreground(lipgloss.Color("10")) // green
	styleUnready     = lipgloss.NewStyle().Foreground(lipgloss.Color("11")) // yellow
	styleError       = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))  // red
	styleStopped     = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))  // gray
	styleBorder      = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	styleBorderFocus = lipgloss.NewStyle().Foreground(lipgloss.Color("12"))
	stylePanelTitle  = lipgloss.NewStyle().Bold(true)
)

func statusStyled(s string) string {
	switch s {
	case state.StatusReady:
		return styleReady.Render(s)
	case state.StatusUnready:
		return styleUnready.Render(s)
	case state.StatusError:
		return styleError.Render(s)
	case state.StatusStopped:
		return styleStopped.Render(s)
	case state.StatusStarting, state.StatusStopping:
		return styleDim.Render(s)
	}
	return s
}

func (m *model) View() string {
	if !m.initialized {
		return "loading…"
	}
	if m.tooSmall() {
		return m.renderHeader() + "\n" +
			styleDim.Render("terminal too small — resize to at least 60x10")
	}
	left := lipgloss.JoinVertical(lipgloss.Left, m.renderAppsPanel(), m.renderDetailPanel())
	main := lipgloss.JoinHorizontal(lipgloss.Top, left, m.renderLogsPanel())
	return m.renderHeader() + "\n" + main + "\n" + m.renderStatusBar()
}

func (m *model) renderHeader() string {
	left := styleHeader.Render("SPM4A")
	nsLabel := "ns: " + m.ns
	if m.all {
		nsLabel = "all namespaces"
	}
	ver := strings.TrimPrefix(m.daemonVersion, "v")
	if ver == "" {
		ver = "?"
	}
	right := styleDim.Render(fmt.Sprintf("%s • v%s • apps: %d", nsLabel, ver, len(m.apps)))
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

// panel draws a square border with the title embedded in the top edge and a
// one-column left margin inside.
func panel(title, content string, width int, focused bool) string {
	bs := styleBorder
	if focused {
		bs = styleBorderFocus
	}
	if width < 10 {
		width = 10
	}
	innerW := width - 2
	textW := innerW - 1 // left margin
	var b strings.Builder
	// top: ┌─ title ──…──┐
	dashCount := width - lipgloss.Width(title) - 5
	if dashCount < 1 {
		title = truncate.String(title, uint(max(width-5, 1)))
		dashCount = 1
	}
	b.WriteString(bs.Render("┌─ ") + stylePanelTitle.Render(title) + bs.Render(" "+strings.Repeat("─", dashCount)+"┐"))
	b.WriteString("\n")
	for _, line := range strings.Split(strings.TrimRight(content, "\n"), "\n") {
		if w := lipgloss.Width(line); w > textW {
			line = truncate.String(line, uint(textW))
			w = textW
		} else {
			line += strings.Repeat(" ", textW-w)
		}
		b.WriteString(bs.Render("│") + " " + line + bs.Render("│"))
		b.WriteString("\n")
	}
	b.WriteString(bs.Render("└" + strings.Repeat("─", innerW) + "┘"))
	return b.String()
}

func (m *model) renderAppsPanel() string {
	nsLabel := m.ns
	if m.all {
		nsLabel = "all"
	}
	title := fmt.Sprintf(" Apps(%s) ", nsLabel)
	if m.focus == focusList {
		title += "* "
	}
	return panel(title, m.renderTable(), m.leftWidth(), m.focus == focusList)
}

func (m *model) renderDetailPanel() string {
	name := "-"
	if a := m.selected(); a != nil {
		name = keyOf(a.Spec.Namespace, a.Spec.Name)
	}
	title := " Detail: " + name + " "
	if m.focus == focusDetail {
		title += "* "
	}
	return panel(title, m.detailVP.View(), m.leftWidth(), m.focus == focusDetail)
}

func (m *model) renderLogsPanel() string {
	name := m.logKey
	if name == "" {
		name = "-"
	}
	title := " Logs: " + name + " "
	if m.focus == focusLogs {
		title += "* "
	}
	return panel(title, m.viewport.View(), m.width-m.leftWidth(), m.focus == focusLogs)
}

// renderTable renders the compact app list (name + colored status), padded to
// exactly appsRows() lines so the left column keeps its height. In -A mode
// names are shown as <ns>/<name> instead of using group headers.
func (m *model) renderTable() string {
	var lines []string
	lines = append(lines, styleHeader.Render(padRight("NAME", m.nameWidth())+"STATUS"))
	if len(m.apps) == 0 {
		lines = append(lines, styleDim.Render("(no apps — spm4a start ...)"))
	} else {
		rows := make([]string, len(m.apps))
		for i, a := range m.apps {
			rows[i] = m.renderRow(i, a)
		}
		visible := m.appsRows() - 1 // minus the column header
		if visible < 1 {
			visible = 1
		}
		start := m.listOffset
		if start > len(rows) {
			start = len(rows)
		}
		end := start + visible
		if end > len(rows) {
			end = len(rows)
		}
		lines = append(lines, rows[start:end]...)
	}
	for len(lines) < m.appsRows() {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func (m *model) renderRow(i int, a *state.App) string {
	nameW := m.nameWidth()
	name := a.Spec.Name
	if m.all {
		name = a.Spec.Namespace + "/" + name
	}
	name = truncate.String(name, uint(max(nameW, 1)))
	row := padRight(name, nameW) + statusStyled(a.Status)
	if i == m.cursor {
		// selection is a full-width reverse highlight, not a ">" prefix, so
		// every row starts at the same column
		return styleSelected.Render(padRight(row, m.rowWidth()))
	}
	return row
}

// rowWidth: usable text width inside the Apps panel (borders + left margin).
func (m *model) rowWidth() int {
	w := m.leftWidth() - 3
	if w < 4 {
		w = 4
	}
	return w
}

// nameWidth: columns left for the app name (row width minus the status column).
func (m *model) nameWidth() int {
	w := m.rowWidth() - 10
	if w < 4 {
		w = 4
	}
	return w
}

// renderDetail: config + status properties of the selected app, shown in the
// bottom-left panel (scrollable when focused).
func (m *model) renderDetail() string {
	a := m.selected()
	if a == nil {
		return styleDim.Render("(no app selected)")
	}
	var lines []string
	kv := func(k, v string) {
		if v == "" {
			v = "-"
		}
		lines = append(lines, styleDim.Render(k+": ")+v)
	}
	kv("name", a.Spec.Name)
	kv("namespace", a.Spec.Namespace)
	lines = append(lines, styleDim.Render("status: ")+statusStyled(a.Status))
	pid, uptime := "-", "-"
	if a.PID != 0 {
		pid = fmt.Sprint(a.PID)
	}
	if !a.StartedAt.IsZero() && a.Active() {
		uptime = time.Since(a.StartedAt).Round(time.Second).String()
	}
	kv("pid", pid)
	kv("uptime", uptime)
	cpu, mem := "-", "-"
	if a.PID != 0 && a.Active() {
		if met, ok := m.metrics[int32(a.PID)]; ok && met.alive {
			cpu = fmt.Sprintf("%.0f%%", met.cpu)
			mem = humanBytes(met.rss)
		}
	}
	kv("cpu", cpu)
	kv("mem", mem)
	kv("restarts", fmt.Sprint(a.Restarts))
	port := "-"
	if a.ActualPort != 0 {
		port = fmt.Sprint(a.ActualPort)
	} else if a.Spec.Port != 0 {
		port = fmt.Sprint(a.Spec.Port)
	}
	kv("port", port)
	if a.Spec.Debug || a.DebugPort != 0 {
		kv("debug", fmt.Sprint(a.DebugPort))
	}
	kv("health", a.Spec.HealthPath)
	kv("workdir", a.Spec.Workdir)
	kv("launcher", a.Spec.Launcher)
	if a.Spec.Launcher == "custom" {
		kv("command", strings.Join(a.Spec.Command, " "))
	} else {
		jar := a.ResolvedJar
		if jar == "" {
			jar = a.Spec.Jar
		}
		kv("jar", jar)
		jdk := a.JavaBin
		if jdk == "" {
			jdk = a.Spec.JDK
		}
		kv("jdk", jdk)
		if a.Spec.Xms != "" || a.Spec.Xmx != "" {
			kv("heap", a.Spec.Xms+" / "+a.Spec.Xmx)
		}
		jvmOpts := a.ResolvedJvmOpts
		if len(jvmOpts) == 0 {
			jvmOpts = a.Spec.JvmOpts
		}
		if len(jvmOpts) > 0 {
			kv("jvmOpts", strings.Join(jvmOpts, " "))
		}
		if len(a.Spec.Args) > 0 {
			kv("args", strings.Join(a.Spec.Args, " "))
		}
	}
	kv("ephemeral", fmt.Sprint(a.Spec.Ephemeral))
	if len(a.Spec.Env) > 0 {
		keys := make([]string, 0, len(a.Spec.Env))
		for k := range a.Spec.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		lines = append(lines, styleDim.Render("env:"))
		for _, k := range keys {
			lines = append(lines, "  "+k+"="+a.Spec.Env[k])
		}
	}
	kv("log", a.LogPath)
	if !a.StartedAt.IsZero() {
		kv("started", a.StartedAt.Format("2006-01-02 15:04:05"))
	}
	if a.LastExit != nil {
		kv("last exit", fmt.Sprintf("code %d at %s", a.LastExit.Code, a.LastExit.At.Format("2006-01-02 15:04:05")))
	}
	return strings.Join(lines, "\n")
}

func (m *model) renderStatusBar() string {
	var msg string
	if m.confirm != nil {
		msg = styleErr.Render(fmt.Sprintf("delete %s/%s? [y/n]", m.confirm.Spec.Namespace, m.confirm.Spec.Name))
	} else if m.statusMsg != "" {
		if m.statusErr {
			msg = styleErr.Render(m.statusMsg)
		} else {
			msg = styleStatusBar.Render(m.statusMsg)
		}
	}
	hint := styleDim.Render("↑↓/jk move • tab focus • enter logs • s stop • r restart • l reload • d rm • A all-ns • q quit")
	if m.focus != focusList {
		hint = styleDim.Render("↑↓/jk scroll • pgup/pgdn page • g/G top/bottom • esc back • q quit")
	}
	if msg == "" {
		return hint
	}
	return msg + "  " + hint
}

func padRight(s string, w int) string {
	if vis := lipgloss.Width(s); vis < w {
		return s + strings.Repeat(" ", w-vis)
	}
	return s
}

func humanBytes(n uint64) string {
	const mi = 1 << 20
	if n >= mi {
		return fmt.Sprintf("%dM", n/mi)
	}
	return fmt.Sprintf("%dK", n>>10)
}
