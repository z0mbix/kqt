package tui

import (
	"fmt"
	"strings"
	"unicode"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

type documentMatch struct {
	line  int
	spans []logSpan
}

type documentSearchState struct {
	query, previousQuery             string
	matches                          []documentMatch
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

func (s *documentSearchState) refresh(content string, palette theme, panel *viewport.Model, best bool) {
	line := -1
	if s.index >= 0 && s.index < len(s.matches) {
		line = s.matches[s.index].line
	}
	s.matches, s.index = nil, -1
	query := strings.TrimSpace(s.query)
	if query != "" {
		bestScore := 0
		for i, styled := range strings.Split(content, "\n") {
			plain := ansi.Strip(styled)
			if score, ok := fuzzyScore(query, plain); ok {
				s.matches = append(s.matches, documentMatch{i, fuzzyLineSpans(query, plain)})
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
	s.render(content, palette, panel)
}

func (s *documentSearchState) render(content string, palette theme, panel *viewport.Model) {
	lines := strings.Split(content, "\n")
	for i, match := range s.matches {
		style := palette.style("accent")
		if i == s.index {
			style = palette.selectionStyle().Bold(true)
		}
		lines[match.line] = highlightTextMatches(lines[match.line], match.spans, style)
	}
	x, y := panel.XOffset(), panel.YOffset()
	panel.SetContent(strings.Join(lines, "\n"))
	panel.SetXOffset(x)
	panel.SetYOffset(y)
}

func (s *documentSearchState) show(content string, panel *viewport.Model) {
	if s.index < 0 || s.index >= len(s.matches) {
		return
	}
	match := s.matches[s.index]
	lines := strings.Split(ansi.Strip(content), "\n")
	start := ansi.StringWidth(lines[match.line][:match.spans[0].from])
	end := ansi.StringWidth(lines[match.line][:match.spans[0].to])
	if !panel.SoftWrap {
		panel.EnsureVisible(match.line, start, end)
		return
	}
	// The viewport scrolls by wrapped rows, while matches refer to source lines.
	width := max(1, panel.Width())
	row := start / width
	for _, line := range lines[:match.line] {
		row += max(1, (ansi.StringWidth(line)+width-1)/width)
	}
	panel.SetXOffset(0)
	if row < panel.YOffset() || row >= panel.YOffset()+panel.Height() {
		panel.SetYOffset(row)
	}
}

func (s *documentSearchState) next(content string, palette theme, panel *viewport.Model, step int) {
	if len(s.matches) == 0 {
		return
	}
	s.index = (s.index + step + len(s.matches)) % len(s.matches)
	s.render(content, palette, panel)
	s.show(content, panel)
}

func (m *model) refreshInspectorSearch(best bool) {
	m.inspectorSearch.refresh(m.inspectorContent, m.theme, &m.inspector, best)
}

func (m *model) renderInspectorSearch() {
	m.inspectorSearch.render(m.inspectorContent, m.theme, &m.inspector)
}

func (m *model) showInspectorMatch() {
	m.inspectorSearch.show(m.inspectorContent, &m.inspector)
}

func (m *model) nextInspectorMatch(step int) {
	m.inspectorSearch.next(m.inspectorContent, m.theme, &m.inspector, step)
}

func (m *model) resetInspectorSearch() {
	if m.mode == "inspector-search" {
		m.mode = ""
		m.input.Blur()
		m.input.SetValue("")
		m.input.CharLimit = 256
	}
	m.inspectorSearch = documentSearchState{index: -1}
	m.renderInspectorSearch()
}

func (m *model) showingDetails() bool { return m.mode == "details" || m.mode == "details-search" }

// Resolve the pane once per input event. Each keeps its own query and matches.
func (m *model) activeDocumentSearch() (*documentSearchState, *viewport.Model, string) {
	if m.showingDetails() {
		return &m.detailsSearch, &m.panel, m.detailContent
	}
	return &m.inspectorSearch, &m.inspector, m.inspectorContent
}

func (m *model) resetDetailsSearch() {
	if m.mode == "details-search" {
		m.mode = "details"
		m.input.Blur()
		m.input.SetValue("")
	}
	m.detailsSearch = documentSearchState{index: -1}
	m.detailContent = ""
}

func (m *model) startDocumentSearch() tea.Cmd {
	s, panel, _ := m.activeDocumentSearch()
	s.previousQuery, s.previousIndex = s.query, s.index
	s.previousXOffset, s.previousYOffset = panel.XOffset(), panel.YOffset()
	if m.showingDetails() {
		m.mode = "details-search"
		m.input.Prompt = strings.ToUpper(m.detailFormat) + " / "
	} else {
		m.mode = "inspector-search"
		m.input.Prompt = "Inspector / "
	}
	m.input.CharLimit = 256
	m.input.SetValue(s.query)
	m.input.CursorEnd()
	m.resize()
	return m.input.Focus()
}

func (m *model) documentSearchKey(msg tea.KeyPressMsg) tea.Cmd {
	s, panel, content := m.activeDocumentSearch()
	switch msg.String() {
	case "esc", "enter":
		if msg.String() == "esc" {
			s.query = s.previousQuery
			s.refresh(content, m.theme, panel, true)
			if s.previousIndex >= 0 {
				s.index = min(s.previousIndex, len(s.matches)-1)
			}
			s.render(content, m.theme, panel)
			panel.SetXOffset(s.previousXOffset)
			panel.SetYOffset(s.previousYOffset)
		}
		s.previousQuery = ""
		if m.showingDetails() {
			m.mode = "details"
		} else {
			m.mode = ""
		}
		m.input.Blur()
		m.input.SetValue("")
		return nil
	case "down", "ctrl+n":
		s.next(content, m.theme, panel, 1)
		return nil
	case "up", "ctrl+p":
		s.next(content, m.theme, panel, -1)
		return nil
	}
	return m.updateDocumentSearch(msg)
}

func (m *model) updateDocumentSearch(msg tea.Msg) tea.Cmd {
	before := m.input.Value()
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	if m.input.Value() != before {
		s, panel, content := m.activeDocumentSearch()
		s.query = m.input.Value()
		s.refresh(content, m.theme, panel, true)
		s.show(content, panel)
	}
	return cmd
}

func (s *documentSearchState) label() string {
	if strings.TrimSpace(s.query) == "" {
		return ""
	}
	if len(s.matches) == 0 {
		return " · no matches · /" + s.query
	}
	return fmt.Sprintf(" · %d/%d matches · /%s", s.index+1, len(s.matches), s.query)
}
