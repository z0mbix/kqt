package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/z0mbix/kqt/internal/kube"
	"slices"
	"strings"
)

type historySearch struct {
	entries       []string
	draft         string
	cursor, index int
	failed        bool
}

func (m *model) startHistorySearch() tea.Cmd {
	m.historySearch = &historySearch{entries: slices.Clone(m.settings.CommandHistory), draft: m.input.Value(), cursor: m.input.Position(), index: -1}
	m.mode = "history-search"
	m.completion = completionState{}
	m.input.Prompt = "(reverse-i-search): "
	m.input.SetValue("")
	m.findHistoryMatch(len(m.historySearch.entries)-1, -1)
	m.resize()
	return m.input.Focus()
}

func (m *model) findHistoryMatch(start, direction int) {
	s := m.historySearch
	query := strings.ToLower(m.input.Value())
	for i := start; i >= 0 && i < len(s.entries); i += direction {
		if strings.Contains(strings.ToLower(s.entries[i]), query) {
			s.index, s.failed = i, false
			return
		}
	}
	s.failed = true
}

func (m *model) finishHistorySearch(accept bool) tea.Cmd {
	s := m.historySearch
	value, cursor := s.draft, s.cursor
	if accept && s.index >= 0 {
		value, cursor = s.entries[s.index], len([]rune(s.entries[s.index]))
	}
	m.historySearch = nil
	m.mode = "command"
	m.input.Prompt = ":"
	m.input.SetValue(value)
	m.input.SetCursor(cursor)
	m.commandHistoryIndex = len(m.settings.CommandHistory)
	m.commandDraft, m.commandDraftCursor = value, cursor
	m.resize()
	m.input.SetCursor(cursor)
	return m.input.Focus()
}

func (m *model) historySearchKey(msg tea.KeyPressMsg) tea.Cmd {
	s := m.historySearch
	switch msg.String() {
	case "esc", "ctrl+g":
		return m.finishHistorySearch(false)
	case "ctrl+r", "up":
		m.findHistoryMatch(s.index-1, -1)
		return nil
	case "ctrl+s", "down":
		m.findHistoryMatch(s.index+1, 1)
		return nil
	case "tab", "left", "right":
		if s.index < 0 {
			return nil
		}
		return m.finishHistorySearch(true)
	case "enter":
		if s.index < 0 {
			return nil
		}
		value := s.entries[s.index]
		m.finishHistorySearch(true)
		m.mode = ""
		m.input.Blur()
		return m.executeSubmittedCommand(value)
	}
	before := m.input.Value()
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	if m.input.Value() != before {
		s.index = -1
		m.findHistoryMatch(len(s.entries)-1, -1)
	}
	return cmd
}

func (m *model) historySearchPreview() string {
	s := m.historySearch
	if s == nil || s.index < 0 {
		return m.theme.style("muted").Render(" No matching command · ") + m.shortcuts("", shortcut{"Esc/Ctrl+G", "cancel"})
	}
	label := " Match: :"
	if s.failed {
		label = " No further match · :"
	}
	return m.theme.style("muted").Render(label+kube.Safe(s.entries[s.index])+"  · ") + m.shortcuts("  ", shortcut{"Ctrl+R", "older"}, shortcut{"Ctrl+S", "newer"}, shortcut{"Enter", "run"}, shortcut{"Tab", "edit"}, shortcut{"Esc", "cancel"})
}

func (m *model) resizeHistorySearch() {
	if m.mode == "history-search" {
		m.input.SetWidth(max(1, m.width-ansi.StringWidth(m.input.Prompt)-2))
		m.input.SetCursor(m.input.Position())
	}
}
