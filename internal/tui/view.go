package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/reflow/truncate"

	"spm4a/internal/state"
)

var (
	styleHeader      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	styleGroup       = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("8"))
	styleSelected    = lipgloss.NewStyle().Reverse(true)
	styleStatusBar   = lipgloss.NewStyle().Foreground(lipgloss.Color("7"))
	styleErr         = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	styleDim         = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	styleReady       = lipgloss.NewStyle().Foreground(lipgloss.Color("10")) // green
	styleUnready     = lipgloss.NewStyle().Foreground(lipgloss.Color("11")) // yellow
	styleError       = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))  // red
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
	case state.StatusStarting, state.StatusStopping:
		return styleDim.Render(s)
	}
	return s
}

func (m *model) View() string {
	if !m.initialized {
		return "loading…"
	}
	var b strings.Builder
	b.WriteString(m.renderHeader())
	b.WriteString("\n")
	b.WriteString(m.renderAppsPanel())
	b.WriteString("\n")
	if m.logsCollapsed() {
		b.WriteString(styleDim.Render("logs collapsed (terminal too small)"))
	} else {
		b.WriteString(m.renderLogsPanel())
	}
	b.WriteString("\n")
	b.WriteString(m.renderStatusBar())
	return b.String()
}

func (m *model) renderHeader() string {
	left := styleHeader.Render("spm4a")
	nsLabel := "ns: " + m.ns
	if m.all {
		nsLabel = "all namespaces"
	}
	ver := m.daemonVersion
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

// panel draws a rounded border with the title embedded in the top edge.
func panel(title, content string, width int, focused bool) string {
	bs := styleBorder
	if focused {
		bs = styleBorderFocus
	}
	if width < 10 {
		width = 10
	}
	innerW := width - 2
	var b strings.Builder
	// top: ╭─ title ──…──╮
	dashCount := width - lipgloss.Width(title) - 5
	if dashCount < 1 {
		title = truncate.String(title, uint(max(width-5, 1)))
		dashCount = 1
	}
	b.WriteString(bs.Render("╭─ ") + stylePanelTitle.Render(title) + bs.Render(" "+strings.Repeat("─", dashCount)+"╮"))
	b.WriteString("\n")
	for _, line := range strings.Split(strings.TrimRight(content, "\n"), "\n") {
		if w := lipgloss.Width(line); w > innerW {
			line = truncate.String(line, uint(innerW))
			w = innerW
		} else {
			line += strings.Repeat(" ", innerW-w)
		}
		b.WriteString(bs.Render("│") + line + bs.Render("│"))
		b.WriteString("\n")
	}
	b.WriteString(bs.Render("╰" + strings.Repeat("─", innerW) + "╯"))
	return b.String()
}

func (m *model) renderAppsPanel() string {
	nsLabel := m.ns
	if m.all {
		nsLabel = "all"
	}
	title := fmt.Sprintf(" Apps(%s) ", nsLabel)
	return panel(title, m.renderTable(), m.width, false)
}

func (m *model) renderLogsPanel() string {
	name := m.logKey
	if name == "" {
		name = "-"
	}
	title := " Logs: " + name + " "
	if m.focusLogs {
		title += "* "
	}
	return panel(title, m.viewport.View(), m.width, m.focusLogs)
}

func (m *model) renderTable() string {
	var b strings.Builder
	if m.all {
		fmt.Fprintf(&b, "%s\n", styleHeader.Render(
			padCols("NAMESPACE", "NAME", "STATUS", "PORT", "DEBUG", "PID", "UPTIME", "CPU%", "MEM")))
	} else {
		fmt.Fprintf(&b, "%s\n", styleHeader.Render(
			padCols("NAME", "STATUS", "PORT", "DEBUG", "PID", "UPTIME", "CPU%", "MEM")))
	}
	if len(m.apps) == 0 {
		b.WriteString(styleDim.Render("  (no apps — start one with: spm4a start ...)"))
		return b.String()
	}

	// Build all lines (group headers interleaved in -A mode), then render the
	// visible window [listOffset, listOffset+visible).
	var lines []string
	lastNs := ""
	for i, a := range m.apps {
		if m.all && a.Spec.Namespace != lastNs {
			lastNs = a.Spec.Namespace
			lines = append(lines, styleGroup.Render("─ "+lastNs+" "))
		}
		lines = append(lines, m.renderRow(i, a))
	}
	visible := m.appsRows() - 1 // minus the column header
	if visible < 1 {
		visible = 1
	}
	start := m.listOffset
	if start > len(lines) {
		start = len(lines)
	}
	end := start + visible
	if end > len(lines) {
		end = len(lines)
	}
	for _, l := range lines[start:end] {
		b.WriteString(l)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m *model) renderRow(i int, a *state.App) string {
	port, debug, pid := "-", "-", "-"
	if a.ActualPort != 0 {
		port = fmt.Sprint(a.ActualPort)
	}
	if a.DebugPort != 0 {
		debug = fmt.Sprint(a.DebugPort)
	}
	if a.PID != 0 {
		pid = fmt.Sprint(a.PID)
	}
	uptime := "-"
	if !a.StartedAt.IsZero() && a.Active() {
		uptime = time.Since(a.StartedAt).Round(time.Second).String()
	}
	cpu, mem := "-", "-"
	if a.PID != 0 && a.Active() {
		if met, ok := m.metrics[int32(a.PID)]; ok && met.alive {
			cpu = fmt.Sprintf("%.0f", met.cpu)
			mem = humanBytes(met.rss)
		}
	}
	var row string
	if m.all {
		row = padCols(a.Spec.Namespace, a.Spec.Name, statusStyled(a.Status), port, debug, pid, uptime, cpu, mem)
	} else {
		row = padCols(a.Spec.Name, statusStyled(a.Status), port, debug, pid, uptime, cpu, mem)
	}
	if i == m.cursor {
		return styleSelected.Render(">" + row)
	}
	return " " + row
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
	hint := styleDim.Render("↑↓/jk move • enter logs • s stop • r restart • l reload • d rm • A all-ns • q quit")
	if msg == "" {
		return hint
	}
	return msg + "  " + hint
}

func padCols(cols ...string) string {
	widths := []int{22, 10, 8, 8, 8, 12, 8, 10}
	if len(cols) == 9 {
		// -A mode with leading NAMESPACE column
		widths = []int{14, 22, 10, 8, 8, 8, 12, 8, 10}
	}
	var b strings.Builder
	for i, c := range cols {
		w := widths[i]
		vis := lipgloss.Width(c)
		b.WriteString(c)
		if vis < w {
			b.WriteString(strings.Repeat(" ", w-vis))
		}
	}
	return b.String()
}

func humanBytes(n uint64) string {
	const mi = 1 << 20
	if n >= mi {
		return fmt.Sprintf("%dM", n/mi)
	}
	return fmt.Sprintf("%dK", n>>10)
}
