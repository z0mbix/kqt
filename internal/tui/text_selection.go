package tui

import (
	"strings"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rivo/uniseg"
)

type textPoint struct{ x, y int }
type textRow struct {
	line, from, to int
}
type textSelection struct {
	bounds      paneBounds
	anchor, end textPoint
	rows        []string // Exact visible text at mouse-down, without styling.
	source      []string
	mapRows     []textRow // Wrapped rows point back to their original lines.
	mode        string
	generation  uint64
	logs, moved bool
	logPending  bool
}
type copyFlashMsg struct{ token uint64 }

func (b paneBounds) contains(x, y int) bool {
	return x >= b.x && y >= b.y && x < b.x+b.width && y < b.y+b.height
}
func (m *model) logTextBounds() paneBounds {
	if m.logFullScreen {
		return paneBounds{0, 0, m.panel.Width(), m.panel.Height()}
	}
	return paneBounds{1, m.bodyBounds().y + 1, m.panel.Width(), m.panel.Height()}
}

func (m *model) selectionPane(x, y int) (paneBounds, *viewport.Model, bool) {
	var bounds paneBounds
	var panel *viewport.Model
	switch m.mode {
	case "logs":
		bounds, panel = m.logTextBounds(), &m.panel
	case "events":
		bounds, panel = paneBounds{1, m.bodyBounds().y + 2, m.panel.Width(), m.panel.Height()}, &m.panel
	case "help", "help-search":
		return m.helpPaneAt(x, y)
	case "details", "access", "overview", "forward-details", "edit-error", "edit-review", "operation-error", "rollout-details":
		bounds, panel = paneBounds{3, 3, m.width - 6, m.height - 6}, &m.panel
	case "":
		b := m.inspectorBounds()
		if b.interior(x, y) {
			bounds, panel = paneBounds{b.x + 1, b.y + 1, b.width - 2, b.height - 2}, &m.inspector
		} else {
			b = m.tableBounds()
			bounds = paneBounds{b.x + 1, b.y + 1, b.width - 2, b.height - 2}
		}
	default:
		return bounds, nil, false
	}
	return bounds, panel, bounds.contains(x, y)
}

func screenRows(content string, bounds paneBounds) []string {
	lines := strings.Split(content, "\n")
	rows := make([]string, bounds.height)
	for i := range rows {
		if y := bounds.y + i; y >= 0 && y < len(lines) {
			rows[i] = strings.TrimRight(ansi.Strip(ansi.Cut(lines[y], bounds.x, bounds.x+bounds.width)), " ")
		}
	}
	return rows
}

// Mirror the viewport's cell-based wrap/scroll geometry, while retaining the
// original hard line breaks for clipboard text. Styling and fill padding never
// enter the clipboard.
func viewportTextRows(panel *viewport.Model, height int) ([]string, []textRow) {
	source := strings.Split(ansi.Strip(panel.GetContent()), "\n")
	rows := make([]textRow, height)
	for i := range rows {
		rows[i].line = -1
	}
	virtual, visible := 0, 0
	width := max(1, panel.Width())
	for i, line := range source {
		cells := ansi.StringWidth(line)
		count := 1
		if panel.SoftWrap {
			count = max(1, (cells+width-1)/width)
		}
		for n := 0; n < count; n++ {
			if virtual >= panel.YOffset() && visible < height {
				from := panel.XOffset()
				if panel.SoftWrap {
					from = n * width
				}
				rows[visible] = textRow{i, from, min(cells, from+width)}
				visible++
			}
			virtual++
		}
		if visible == height {
			break
		}
	}
	return source, rows
}

func (m *model) beginSelection(v tea.MouseClickMsg) {
	if v.Button != tea.MouseLeft || v.Mod != 0 || m.width < 40 || m.height < 12 {
		return
	}
	bounds, panel, ok := m.selectionPane(v.X, v.Y)
	if !ok {
		return
	}
	s := &textSelection{bounds: bounds, mode: m.mode, generation: m.generation, logs: m.mode == "logs"}
	s.rows = screenRows(m.baseView().Content, bounds)
	if panel != nil {
		s.source, s.mapRows = viewportTextRows(panel, bounds.height)
	} else {
		s.source = s.rows
		for i, line := range s.rows {
			s.mapRows = append(s.mapRows, textRow{i, 0, ansi.StringWidth(line)})
		}
	}
	s.anchor = textPoint{v.X - bounds.x, v.Y - bounds.y}
	s.end = s.anchor
	if s.mapRows[s.anchor.y].line < 0 {
		return
	}
	m.selection = s
}
func (m *model) moveSelection(v tea.MouseMotionMsg) {
	if m.selection == nil || v.Button != tea.MouseLeft || v.Mod != 0 {
		return
	}
	m.selection.move(v.X, v.Y)
}
func (s *textSelection) move(x, y int) {
	s.end = textPoint{max(0, min(s.bounds.width-1, x-s.bounds.x)), max(0, min(s.bounds.height-1, y-s.bounds.y))}
	s.moved = s.moved || s.end != s.anchor
}
func (s *textSelection) ordered() (textPoint, textPoint) {
	start, end := s.anchor, s.end
	if start.y > end.y || start.y == end.y && start.x > end.x {
		start, end = end, start
	}
	return start, end
}
func (s *textSelection) span(y int) (int, int) {
	start, end := s.ordered()
	left, right := 0, s.bounds.width
	if y == start.y {
		left = start.x
	}
	if y == end.y {
		right = end.x + 1
	}
	return left, right
}

func cellSlice(text string, from, to int) string {
	var out strings.Builder
	position := 0
	clusters := uniseg.NewGraphemes(text)
	for clusters.Next() {
		cluster := clusters.Str()
		next := position + ansi.StringWidth(cluster)
		if next > from && position < to {
			out.WriteString(cluster)
		}
		position = next
		if position >= to {
			break
		}
	}
	return out.String()
}

// A click can land on either cell of a wide glyph. Highlight the whole cluster
// so cutting ANSI text at the selection edge does not replace it with padding.
func cellSpan(text string, from, to int) (int, int) {
	position := 0
	clusters := uniseg.NewGraphemes(text)
	for clusters.Next() {
		next := position + ansi.StringWidth(clusters.Str())
		if position < from && next > from {
			from = position
		}
		if position < to && next > to {
			to = next
		}
		position = next
		if position >= to {
			break
		}
	}
	return from, to
}
func (s *textSelection) text() string {
	start, end := s.ordered()
	var out strings.Builder
	previous, from, to := -1, 0, 0
	flush := func() {
		if previous >= 0 {
			out.WriteString(cellSlice(s.source[previous], from, to))
		}
	}
	for y := start.y; y <= end.y; y++ {
		row := s.mapRows[y]
		if row.line < 0 {
			continue
		}
		left, right := s.span(y)
		left, right = min(row.to, row.from+left), min(row.to, row.from+right)
		if row.line == previous {
			to = right
			continue
		}
		if previous >= 0 {
			flush()
			out.WriteByte('\n')
		}
		previous, from, to = row.line, left, right
	}
	flush()
	return out.String()
}

func (m *model) selectionValid(content string) bool {
	s := m.selection
	if s == nil || s.mode != m.mode || s.generation != m.generation {
		return false
	}
	current := screenRows(content, s.bounds)
	for i := range current {
		if current[i] != s.rows[i] {
			return false
		}
	}
	return true
}
func (m *model) selectingLogs() bool { return m.selection != nil && m.selection.logs }
func (m *model) cancelSelection() {
	pending := m.selectingLogs() && m.selection.logPending
	m.selection = nil
	if pending && m.showingLogs() && !m.logPaused {
		m.renderLogs()
	}
}
func (m *model) finishSelection(v tea.MouseReleaseMsg) tea.Cmd {
	s := m.selection
	if s == nil {
		return nil
	}
	var text string
	if (v.Button == tea.MouseLeft || v.Button == tea.MouseNone) && v.Mod == 0 && m.selectionValid(m.baseView().Content) {
		s.move(v.X, v.Y)
		if s.moved {
			text = s.text()
		}
	}
	m.cancelSelection()
	if text == "" {
		return nil
	}
	if len(text) > 1024*1024 {
		return m.flashCopy("Selection too large")
	}
	return tea.Batch(tea.SetClipboard(text), m.flashCopy("Copied"))
}
func (m *model) flashCopy(text string) tea.Cmd {
	m.copyFlash, m.copyFlashToken = text, m.copyFlashToken+1
	token := m.copyFlashToken
	return tea.Tick(1200*time.Millisecond, func(time.Time) tea.Msg { return copyFlashMsg{token} })
}

func (m *model) View() tea.View {
	v := m.baseView()
	if s := m.selection; s != nil && s.moved && m.selectionValid(v.Content) {
		lines := strings.Split(v.Content, "\n")
		start, end := s.ordered()
		style := m.theme.selectionStyle()
		for y := start.y; y <= end.y; y++ {
			left, right := s.span(y)
			left, right = left+s.bounds.x, right+s.bounds.x
			row := y + s.bounds.y
			line := lines[row]
			left, right = cellSpan(ansi.Strip(line), left, right)
			lines[row] = ansi.Cut(line, 0, left) + style.Render(ansi.Strip(ansi.Cut(line, left, right))) + ansi.Cut(line, right, m.width)
		}
		v.Content = strings.Join(lines, "\n")
	}
	if m.copyFlash != "" && m.width > 0 && m.height > 0 {
		flash := m.theme.style("success").Bold(true).Background(m.theme.color("surface")).Render(" " + m.copyFlash + " ")
		flash = ansi.Truncate(flash, m.width, "")
		v.Content = lipgloss.NewCompositor(lipgloss.NewLayer(v.Content), lipgloss.NewLayer(flash).X(max(0, m.width-ansi.StringWidth(flash))).Y(m.height-1).Z(2)).Render()
	}
	return v
}

func (m *model) toggleLogFullScreen() {
	m.logFullScreen = !m.logFullScreen
	m.resize()
	if m.logTail && !m.logPaused {
		m.panel.GotoBottom()
	}
}
