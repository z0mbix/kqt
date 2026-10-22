package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/z0mbix/kqt/internal/kube"
)

func fit(text string, width int) string {
	width = max(0, width)
	text = ansi.Truncate(text, width, "…")
	return text + strings.Repeat(" ", max(0, width-ansi.StringWidth(text)))
}
func (m *model) frame(title, content string, width, height int, focused bool, hints ...shortcut) string {
	width = max(2, width)
	height = max(2, height)
	inner := width - 2
	b := m.theme.style("border")
	marker := "─ "
	if focused {
		b = m.theme.style("focus").Bold(true)
		marker = "› "
	}
	top := b.Render(marker + kube.Safe(title))
	if len(hints) > 0 {
		top += m.theme.style("muted").Render(" · ") + m.shortcuts(" · ", hints...)
	}
	top = ansi.Truncate(top+" ", inner, "")
	top += b.Render(strings.Repeat("─", max(0, inner-ansi.StringWidth(top))))
	lines := []string{b.Render("╭") + top + b.Render("╮")}
	parts := strings.Split(content, "\n")
	for i := 0; i < height-2; i++ {
		line := ""
		if i < len(parts) {
			line = parts[i]
		}
		lines = append(lines, b.Render("│")+fit(line, inner)+b.Render("│"))
	}
	lines = append(lines, b.Render("╰"+strings.Repeat("─", inner)+"╯"))
	return strings.Join(lines, "\n")
}

type dimensions struct {
	width, height int
	bottom        bool
}

func (m *model) inspectorDimensions() dimensions {
	body := m.bodyBounds().height
	p := m.inspectorProfile()
	rightFits, bottomFits := m.width >= 120 && body >= 8, body >= 18
	if p.Inspector == "right" && rightFits || p.Inspector == "bottom" && !bottomFits && rightFits {
		return dimensions{m.width * p.InspectorPercent / 100, body, false}
	}
	if p.Inspector == "bottom" && bottomFits || p.Inspector == "right" && !rightFits && bottomFits {
		return dimensions{m.width, max(7, body*p.InspectorPercent/100), true}
	}
	return dimensions{}
}
func (m *model) resize() {
	m.input.SetWidth(max(1, m.width-8))
	m.resizeHistorySearch()
	if m.mode == "bundle-save" || m.mode == "manifest-save" || m.showingLogs() && m.mode != "logs" || m.mode == "event-save" || m.mode == "debug-image" {
		m.input.SetWidth(max(1, m.width-ansi.StringWidth(m.input.Prompt)-2))
		m.input.SetCursor(m.input.Position()) // Reflow long paths immediately after a resize.
	}
	if m.mode == "picker" {
		m.input.SetWidth(max(1, m.modalBounds().width-6))
	}
	if m.mode == "namespace-group" {
		m.input.SetWidth(max(1, m.modalBounds().width-4-ansi.StringWidth(m.input.Prompt)))
		m.input.SetCursor(m.input.Position())
	}
	panelWidth, panelHeight := m.width-6, m.height-6
	if m.smallDeleteModal() {
		b := m.modalBounds()
		panelWidth, panelHeight = b.width-2, b.height-4
	}
	if m.showingLogs() || m.showingEventPanel() {
		panelWidth, panelHeight = m.width-2, m.bodyBounds().height-2
	}
	if m.showingEventPanel() {
		panelHeight-- // Pinned resource tabs above the scrolling viewport.
	}
	if m.showingLogs() && m.logFullScreen {
		panelWidth, panelHeight = m.width, m.height
		if m.mode != "logs" {
			panelHeight--
		}
	}
	if m.showingHelp() {
		panes := m.helpPanes()
		panelWidth, panelHeight = panes.shortcuts.width, panes.shortcuts.height
		m.helpNotes.SetWidth(panes.notes.width)
		m.helpNotes.SetHeight(panes.notes.height)
	}
	m.panel.SetWidth(max(1, panelWidth))
	m.panel.SetHeight(max(1, panelHeight))
	d := m.inspectorDimensions()
	m.inspector.SetWidth(max(1, d.width-2))
	m.inspector.SetHeight(max(1, d.height-2))
	if d.width == 0 {
		m.inspectorFocused = false
	}
	if m.showingHelp() {
		m.refreshHelp(false)
	}
}
func (m *model) statusToken(phase string) string {
	switch strings.ToLower(phase) {
	case "ready", "running", "active", "bound", "succeeded", "normal":
		return "success"
	case "failed", "notready", "crashloopbackoff", "imagepullbackoff", "errimagepull", "error", "oomkilled":
		return "error"
	case "pending", "progressing", "terminating", "containercreating", "warning":
		return "warning"
	}
	return "foreground"
}
func heading(c string) string {
	if strings.HasPrefix(c, "/") {
		parts := strings.Split(c, "/")
		return strings.ToUpper(parts[len(parts)-1])
	}
	return strings.ToUpper(c)
}

type tableGeometry struct {
	columns                  []string
	widths                   []int
	total, offset, dataWidth int
}

func (m *model) tableGeometry(width int) tableGeometry {
	cols := m.columns()
	widths := make([]int, len(cols))
	desired := make([]int, len(cols))
	total := 2
	inner := width - 2
	limit := 64
	if m.wide {
		limit = max(limit, inner-2)
	}
	for i, c := range cols {
		widths[i] = min(12, max(4, ansi.StringWidth(heading(c))+2))
		desired[i] = widths[i]
		for _, o := range m.visible {
			desired[i] = max(desired[i], min(limit, ansi.StringWidth(m.tableCellText(o, c))))
		}
		if c == "name" {
			desired[i] = min(limit, desired[i]+1)
		}
		if m.wide {
			// Keep useful widths when scrolling a narrow table, then let long
			// fields share any spare space instead of stopping at 64 cells.
			widths[i] = min(64, desired[i])
		}
		total += widths[i] + 1
	}
	if !m.wide {
		for i, c := range cols {
			if c == "name" {
				growth := min(desired[i]-widths[i], max(0, inner-total))
				widths[i] += growth
				total += growth
			}
		}
	}
	for total < inner {
		changed := false
		for i := range widths {
			if widths[i] < desired[i] {
				widths[i]++
				total++
				changed = true
				if total >= inner {
					break
				}
			}
		}
		if !changed {
			break
		}
	}
	x := min(m.horizontal, max(0, total-inner))
	return tableGeometry{cols, widths, total, x, max(0, inner-2)}
}

func tableStart(selected, count, capacity int) int {
	return min(max(0, selected-capacity+1), max(0, count-capacity))
}

func (m *model) table(width, height int) string {
	geometry := m.tableGeometry(width)
	cols, widths, total, x := geometry.columns, geometry.widths, geometry.total, geometry.offset
	inner := width - 2
	chosen := m.orderColumn()
	var cells []string
	for i, c := range cols {
		h := heading(c)
		if c == m.sortColumn {
			if m.descending {
				h += " ↓"
			} else {
				h += " ↑"
			}
		}
		cell := fit(h, widths[i])
		if c == chosen {
			cell = m.theme.style("focus").Bold(true).Underline(true).Render(cell)
		}
		cells = append(cells, cell)
	}
	lines := []string{m.theme.style("accent").Bold(true).Render(fit("  "+ansi.Cut(strings.Join(cells, " "), x, x+max(0, inner-2)), inner))}
	capacity := max(0, height-3)
	start := tableStart(m.selected, len(m.visible), capacity)
	for i := start; i < min(len(m.visible), start+capacity); i++ {
		o := m.visible[i]
		cells = nil
		for j, c := range cols {
			value := fit(m.tableCellText(o, c), widths[j])
			if c == "labels" {
				value = m.theme.renderTableLabels(value, i == m.selected)
			}
			if (c == "status" || o.GetKind() == "Event" && c == "type") && i != m.selected {
				value = m.theme.style(m.statusToken(kube.Status(o))).Render(value)
			}
			cells = append(cells, value)
		}
		marker := "  "
		if i == m.selected {
			marker = "› "
		}
		line := fit(marker+ansi.Cut(strings.Join(cells, " "), x, x+max(0, inner-2)), inner)
		if i == m.selected {
			line = m.theme.selectionStyle().Bold(true).Render(line)
		}
		lines = append(lines, line)
	}
	if len(m.visible) == 0 && capacity > 0 {
		text := "No resources in this scope"
		if m.busy {
			text = "Connecting / loading…"
		} else if m.filter != "" {
			text = "No resources match this filter"
		} else if m.dataErr != "" {
			text = "Unable to load resources · Ctrl+R retries"
		} else if m.contextName == "" {
			text = "Choose a context with c"
		}
		lines = append(lines, m.theme.style("muted").Render(text))
	}
	title := m.route.resource.ID()
	if title == "" {
		title = "resources"
	}
	title += fmt.Sprintf(" [%d]", len(m.visible))
	for _, scope := range []string{m.route.labels, m.route.fields} {
		if scope != "" {
			title += " · " + scope
		}
	}
	if m.filter != "" {
		title += " / " + m.filter
	}
	if m.wide {
		title += " · wide"
	}
	if m.eventGlobal && m.route.resource.Kind == "Event" {
		title += " · " + m.eventViewState()
	}
	if total > inner {
		title += fmt.Sprintf(" · scroll %d/%d", x, total-inner)
	}
	return m.frame(title, strings.Join(lines, "\n"), width, height, !m.inspectorFocused)
}
func (m *model) inspectorView(d dimensions) string {
	title := "Inspector · " + strings.ToUpper(m.inspectorFormat)
	hints := []shortcut{{"Tab", "focus"}}
	if m.inspectorFocused {
		title = fmt.Sprintf("Inspector · focused · %s · %.0f%%", strings.ToUpper(m.inspectorFormat), m.inspector.ScrollPercent()*100)
		hints = nil
	}
	if o, ok := m.current(); ok && o.GetKind() == "Secret" {
		title = "Inspector · " + m.secretLabel() + " · " + strings.ToUpper(m.inspectorFormat)
		hints = nil
		if m.inspectorFocused {
			title = "Inspector · focused · " + m.secretLabel() + " · " + strings.ToUpper(m.inspectorFormat)
		}
	}
	return m.frame(title, m.inspector.View(), d.width, d.height, m.inspectorFocused, hints...)
}
func (m *model) pickerView(width, height int) string {
	var lines []string
	lines = append(lines, fit(m.input.View(), width-2), "")
	capacity := max(1, height-6)
	if m.pickerKind == "namespace-groups" {
		capacity = max(1, height-8)
	}
	start := min(max(0, m.pickerIndex-capacity+1), max(0, len(m.matches)-capacity))
	for i := start; i < min(len(m.matches), start+capacity); i++ {
		c := m.matches[i]
		label := kube.Safe(c.name) + " · " + kube.Safe(c.description)
		if m.pickerKind == "namespace-groups" {
			label = "@" + label
		}
		if m.pickerKind == "relations" {
			label = kube.Safe(c.description)
		}
		text := "  " + label
		if i == m.pickerIndex {
			text = "› " + label
			text = m.theme.selectionStyle().Render(fit(text, width-2))
		}
		lines = append(lines, text)
	}
	if len(m.matches) == 0 {
		if m.pickerKind == "namespace-groups" && len(m.choices) == 0 {
			lines = append(lines, "No saved groups in this context.")
		} else {
			lines = append(lines, "No matches")
		}
	}
	hints := []shortcut{{"↑/↓ Ctrl+J/K", "move"}, {"Enter", "select"}, {"Esc", "close"}}
	if m.pickerKind == "inspector-position" {
		hints = []shortcut{{"↑/↓", "preview"}, {"Enter", "save"}, {"Esc", "cancel"}}
	}
	if m.pickerKind == "forwards" {
		hints = []shortcut{{"↑/↓", "select"}, {"x", "stop"}, {"Esc", "close"}}
	}
	if m.pickerKind == "rollouts" {
		hints = []shortcut{{"↑/↓", "select"}, {"Enter", "details"}, {"Ctrl+R", "reload"}, {"Esc", "close"}}
	}
	hintLine := m.shortcuts(" · ", hints...)
	if m.pickerKind != "inspector-position" {
		hintLine = m.theme.style("muted").Render(" Type to search · ") + hintLine
	}
	lines = append(lines, "", hintLine)
	if m.pickerKind == "namespace-groups" {
		lines = append(lines, m.shortcuts(" · ", shortcut{"Ctrl+N", "save current"}, shortcut{"Ctrl+E", "edit"}, shortcut{"Ctrl+R", "rename"}, shortcut{"Ctrl+D", "delete"}))
	}
	title := strings.Title(m.pickerKind)
	if m.pickerKind == "namespace-groups" {
		title = "Namespace groups · " + m.contextLabel(m.contextName)
	}
	if m.pickerKind == "inspector-position" {
		title = "Inspector position · preview"
	}
	if m.pickerKind == "debug-image-source" {
		title = "Debug image · first use"
	}
	if m.pickerKind == "log-options" {
		title = "Log options · saved as defaults"
	}
	return m.frame(title, strings.Join(lines, "\n"), width, height, true)
}
func (m *model) baseView() tea.View {
	w, h := m.width, m.height
	var content string
	if m.showingLogs() && m.logFullScreen {
		parts := strings.Split(m.panel.View(), "\n")
		lines := make([]string, m.panel.Height())
		for i := range lines {
			if i < len(parts) {
				lines[i] = fit(parts[i], w)
			} else {
				lines[i] = fit("", w)
			}
		}
		if m.mode != "logs" {
			lines = append(lines, fit(m.input.View(), w))
		}
		content = strings.Join(lines, "\n")
	} else if w < 40 || h < 12 {
		content = fit("kqt needs at least 40×12. Resize or press q.", w)
	} else {
		var lines []string
		if m.headerVisible() {
			lines = append(lines, m.headerView())
		}
		bodyHeight := m.bodyBounds().height
		d := m.inspectorDimensions()
		body := ""
		if m.showingLogs() || m.showingEventPanel() {
			hints := []shortcut{{"Esc", "close"}, {"hjkl", "scroll"}, {"Ctrl+R", "reload"}}
			if m.showingLogs() {
				hints = []shortcut{{"Esc", "close"}, {"hjkl", "scroll"}, {"c", "container"}, {"p", "previous"}, {"Ctrl+R", "reload"}}
			}
			panel := m.panel.View()
			if m.showingEventPanel() {
				panel = m.resourcePanelTabs() + "\n" + panel
			}
			body = m.frame(m.panelTitle, panel, w, bodyHeight, true, hints...)
			body = lipgloss.NewStyle().Background(m.theme.color("surface")).Foreground(m.theme.color("foreground")).Render(body)
		} else if d.width > 0 {
			if d.bottom {
				body = m.table(w, bodyHeight-d.height) + "\n" + m.inspectorView(d)
			} else {
				body = lipgloss.JoinHorizontal(lipgloss.Top, m.table(w-d.width, bodyHeight), m.inspectorView(d))
			}
		} else {
			body = m.table(w, bodyHeight)
		}
		lines = append(lines, body)
		status, token := m.notice, "warning"
		if status == "" {
			status = m.settings.historyWarning
		}
		if status == "" {
			status = m.metricsWarning
		}
		if status == "" {
			status = m.discoveryWarning
		}
		if status == "" {
			status = fmt.Sprintf(" %d/%d resources · sort %s · %s", len(m.visible), len(m.rows), m.sortColumn, m.layout)
			if !m.lastRefresh.IsZero() {
				status += " · refreshed " + m.lastRefresh.Format("15:04:05")
			}
			if m.route.resource.Kind != "Event" {
				status += " · " + m.resourceWatchLabel()
			}
			token = "muted"
		}
		if m.dataErr != "" {
			status = " STALE · " + m.dataErr
			token = "error"
		} else if m.namespaceWarning != "" {
			status = " " + m.namespaceWarning
			token = "warning"
		}
		if m.secretTarget != "" {
			status = " Secret " + m.secretLabel() + " · " + status
			if m.dataErr == "" {
				token = "warning"
			}
		}
		if m.eventGlobal && m.route.resource.Kind == "Event" && m.notice == "" {
			status = " Events · " + m.eventViewState()
			if m.eventError != "" {
				status += " · " + m.eventError
				token = "warning"
			}
			if dropped := m.eventDropped + m.eventStore.dropped; dropped > 0 {
				status += fmt.Sprintf(" · %d records omitted", dropped)
			}
			if m.eventGaps > 0 {
				status += " · resynced; history may have gaps"
			}
		}
		if m.mode == "help-search" || m.mode == "command" || m.mode == "history-search" || m.mode == "filter" || m.showingLogs() && m.mode != "logs" || m.mode == "event-search" || m.mode == "event-save" || m.mode == "bundle-save" || m.mode == "manifest-save" || m.mode == "action-confirm" || m.mode == "debug-image" {
			status = m.input.View()
			token = "foreground"
		}
		lines = append(lines, m.theme.style(token).Render(fit(status, w)))
		lines = append(lines, m.theme.style("muted").Render(fit(m.footerHelp(d), w)))
		content = strings.Join(lines, "\n")
		if m.mode == "namespace-group" || m.mode == "bundle-loading" || m.mode == "picker" || m.mode == "details" || m.showingHelp() || m.mode == "edit-error" || m.mode == "edit-review" || m.mode == "operation-error" || m.mode == "forward-details" || m.mode == "relations-loading" || m.mode == "rollouts-loading" || m.mode == "rollout-details" || m.mode == "action-confirm" || m.mode == "delete-confirm" || m.mode == "action-running" || m.mode == "access" || m.mode == "access-loading" || m.mode == "overview" || m.showingDebug() {
			bounds := m.modalBounds()
			mw, mh := bounds.width, bounds.height
			modal := ""
			if m.mode == "namespace-group" {
				modal = m.namespaceGroupView(mw, mh)
			} else if m.mode == "picker" {
				mw = min(90, mw)
				mh = min(24, mh)
				modal = m.pickerView(mw, mh)
			} else {
				hints := []shortcut{{"Esc", "close"}, {"hjkl", "scroll"}}
				if m.showingHelp() {
					hints = []shortcut{{"/", "search"}, {"Tab", "pane"}, {"Esc", "close"}}
					if m.mode == "help-search" {
						hints = []shortcut{{"Enter", "keep filter"}, {"Esc", "cancel"}, {"↑/↓", "scroll"}}
					}
				} else if m.mode == "edit-review" {
					hints = []shortcut{{"Enter", "apply"}, {"Esc", "discard"}, {"hjkl", "scroll"}}
				} else if m.mode == "debug-image" {
					hints = []shortcut{{"Enter", "save and continue"}, {"Esc", "cancel"}}
				} else if m.mode == "debug-confirm" {
					hints = []shortcut{{"Enter", "start"}, {"c", "container"}, {"d", "details"}, {"Esc", "cancel"}}
				} else if m.mode == "action-confirm" {
					hints = []shortcut{{"Enter", "confirm"}, {"Esc", "cancel"}}
				} else if m.mode == "rollout-details" {
					hints = []shortcut{{"d", "compare"}, {"y", "template"}, {"r", "rollback"}, {"Esc", "close"}, {"hjkl", "scroll"}}
				}
				body := m.panel.View()
				if m.showingHelp() {
					body = m.helpView()
				}
				if m.mode == "delete-confirm" {
					hints = []shortcut{{"←/→", "choose"}, {"Enter", "confirm"}, {"Esc", "cancel"}}
					body += "\n\n" + m.deleteButtons()
				}
				modal = m.frame(m.panelTitle, body, mw, mh, true, hints...)
			}
			modal = lipgloss.NewStyle().Background(m.theme.color("surface")).Foreground(m.theme.color("foreground")).Render(modal)
			content = lipgloss.NewCompositor(lipgloss.NewLayer(content), lipgloss.NewLayer(modal).X(bounds.x).Y(bounds.y).Z(1)).Render()
			// The compositor trims unstyled trailing spaces after colour resets.
			// Keep every row aligned with the terminal and mouse coordinates.
			rows := strings.Split(content, "\n")
			for i, row := range rows {
				rows[i] = fit(row, w)
			}
			content = strings.Join(rows, "\n")
		}
	}
	v := tea.NewView(content)
	v.AltScreen = true
	if w >= 40 && h >= 12 && m.mode != "shell" && m.mode != "editing" && m.mode != "edit-loading" {
		v.MouseMode = tea.MouseModeCellMotion
	}
	if m.theme.Colors["background"] != "default" {
		v.BackgroundColor = m.theme.color("background")
	}
	if m.theme.Colors["foreground"] != "default" {
		v.ForegroundColor = m.theme.color("foreground")
	}
	return v
}
