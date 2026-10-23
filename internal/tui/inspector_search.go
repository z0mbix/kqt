package tui

import (
	"fmt"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

type inspectorMatch struct {
	line  int
	spans []logSpan
}

type inspectorSearchState struct {
	query, previousQuery             string
	matches                          []inspectorMatch
	index, previousIndex             int
	previousXOffset, previousYOffset int
}

// Use the same greedy, case-insensitive subsequence as fuzzyScore, recording
// byte positions in the original text so Unicode and syntax styling survive.
func fuzzyLineSpans(query, line string) []logSpan {
	needle := []rune(strings.ToLower(query))
	var spans []logSpan
	pos := 0
	for offset, r := range line {
		if pos == len(needle) {
			break
		}
		if unicode.ToLower(r) == needle[pos] {
			spans = append(spans, logSpan{offset, offset + len(string(r))})
			pos++
		}
	}
	if pos != len(needle) {
		return nil
	}
	return spans
}

func (m *model) refreshInspectorSearch(best bool) {
	s := &m.inspectorSearch
	line := -1
	if s.index >= 0 && s.index < len(s.matches) {
		line = s.matches[s.index].line
	}
	s.matches, s.index = nil, -1
	query := strings.TrimSpace(s.query)
	if query != "" {
		bestScore := 0
		for i, styled := range strings.Split(m.inspectorContent, "\n") {
			plain := ansi.Strip(styled)
			if score, ok := fuzzyScore(query, plain); ok {
				s.matches = append(s.matches, inspectorMatch{i, fuzzyLineSpans(query, plain)})
				if best && (s.index == -1 || score > bestScore) || !best && s.index == -1 && i >= line {
					s.index = len(s.matches) - 1
					bestScore = score
				}
			}
		}
		if s.index == -1 && len(s.matches) > 0 {
			s.index = len(s.matches) - 1
		}
	}
	m.renderInspectorSearch()
}

func (m *model) renderInspectorSearch() {
	s := &m.inspectorSearch
	lines := strings.Split(m.inspectorContent, "\n")
	for i, match := range s.matches {
		style := m.theme.style("accent")
		if i == s.index {
			style = m.theme.selectionStyle().Bold(true)
		}
		lines[match.line] = highlightTextMatches(lines[match.line], match.spans, style)
	}
	x, y := m.inspector.XOffset(), m.inspector.YOffset()
	m.inspector.SetContent(strings.Join(lines, "\n"))
	m.inspector.SetXOffset(x)
	m.inspector.SetYOffset(y)
}

func (m *model) showInspectorMatch() {
	s := &m.inspectorSearch
	if s.index < 0 || s.index >= len(s.matches) {
		return
	}
	match := s.matches[s.index]
	lines := strings.Split(ansi.Strip(m.inspectorContent), "\n")
	start := ansi.StringWidth(lines[match.line][:match.spans[0].from])
	end := ansi.StringWidth(lines[match.line][:match.spans[0].to])
	if !m.inspector.SoftWrap {
		m.inspector.EnsureVisible(match.line, start, end)
		return
	}
	// The viewport scrolls by wrapped rows, while matches refer to source lines.
	width := max(1, m.inspector.Width())
	row := start / width
	for _, line := range lines[:match.line] {
		row += max(1, (ansi.StringWidth(line)+width-1)/width)
	}
	m.inspector.SetXOffset(0)
	if row < m.inspector.YOffset() || row >= m.inspector.YOffset()+m.inspector.Height() {
		m.inspector.SetYOffset(row)
	}
}

func (m *model) nextInspectorMatch(step int) {
	s := &m.inspectorSearch
	if len(s.matches) == 0 {
		return
	}
	s.index = (s.index + step + len(s.matches)) % len(s.matches)
	m.renderInspectorSearch()
	m.showInspectorMatch()
}

func (m *model) resetInspectorSearch() {
	if m.mode == "inspector-search" {
		m.mode = ""
		m.input.Blur()
		m.input.SetValue("")
		m.input.CharLimit = 256
	}
	m.inspectorSearch = inspectorSearchState{index: -1}
	m.renderInspectorSearch()
}

func (m *model) startInspectorSearch() tea.Cmd {
	s := &m.inspectorSearch
	s.previousQuery, s.previousIndex = s.query, s.index
	s.previousXOffset, s.previousYOffset = m.inspector.XOffset(), m.inspector.YOffset()
	m.mode = "inspector-search"
	m.input.Prompt = "Inspector / "
	m.input.CharLimit = 256
	m.input.SetValue(s.query)
	m.input.CursorEnd()
	m.resize()
	return m.input.Focus()
}

func (m *model) inspectorSearchKey(msg tea.KeyPressMsg) tea.Cmd {
	s := &m.inspectorSearch
	switch msg.String() {
	case "esc", "enter":
		if msg.String() == "esc" {
			s.query = s.previousQuery
			m.refreshInspectorSearch(true)
			if s.previousIndex >= 0 {
				s.index = min(s.previousIndex, len(s.matches)-1)
			}
			m.renderInspectorSearch()
			m.inspector.SetXOffset(s.previousXOffset)
			m.inspector.SetYOffset(s.previousYOffset)
		}
		s.previousQuery = ""
		m.mode = ""
		m.input.Blur()
		m.input.SetValue("")
		return nil
	case "down", "ctrl+n":
		m.nextInspectorMatch(1)
		return nil
	case "up", "ctrl+p":
		m.nextInspectorMatch(-1)
		return nil
	}
	return m.updateInspectorSearch(msg)
}

func (m *model) updateInspectorSearch(msg tea.Msg) tea.Cmd {
	before := m.input.Value()
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	if m.input.Value() != before {
		m.inspectorSearch.query = m.input.Value()
		m.refreshInspectorSearch(true)
		m.showInspectorMatch()
	}
	return cmd
}

func (m *model) inspectorSearchLabel() string {
	s := &m.inspectorSearch
	if strings.TrimSpace(s.query) == "" {
		return ""
	}
	if len(s.matches) == 0 {
		return " · no matches · /" + s.query
	}
	return fmt.Sprintf(" · %d/%d matches · /%s", s.index+1, len(s.matches), s.query)
}
