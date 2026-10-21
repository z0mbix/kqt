package tui

import (
	"fmt"
	"slices"
	"strings"

	"github.com/z0mbix/kqt/internal/kube"
)

const defaultHistoryLimit = 100

func validateHistoryLimit(limit int) error {
	if limit < 1 {
		return fmt.Errorf("history_limit must be a positive integer")
	}
	return nil
}

type historySuccess struct {
	sequence uint64
	value    string
}

func (m *model) nextCommandSequence() uint64 {
	if m.historySuccesses == nil {
		for _, value := range m.settings.CommandHistory {
			m.commandSequence++
			m.historySuccesses = append(m.historySuccesses, historySuccess{m.commandSequence, value})
		}
	}
	m.commandSequence++
	return m.commandSequence
}

func boundedCommandHistory(values []string, limit int) []string {
	var history []string
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			history = append(history, value)
		}
	}
	return history[max(0, len(history)-max(0, limit)):]
}

func (m *model) rememberCommand(value string) {
	m.rememberCommandAt(value, m.nextCommandSequence())
}
func (m *model) rememberCommandAt(value string, sequence uint64) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	for _, item := range m.historySuccesses {
		if item.sequence == sequence {
			return
		}
	}
	m.historySuccesses = append(m.historySuccesses, historySuccess{sequence, value})
	slices.SortFunc(m.historySuccesses, func(a, b historySuccess) int {
		if a.sequence < b.sequence {
			return -1
		}
		if a.sequence > b.sequence {
			return 1
		}
		return 0
	})
	m.historySuccesses = m.historySuccesses[max(0, len(m.historySuccesses)-m.settings.Config.HistoryLimit):]
	var history []string
	for _, item := range m.historySuccesses {
		history = append(history, item.value)
	}
	m.settings.CommandHistory = history
	m.commandHistoryIndex = len(history)
	if err := m.settings.saveCommandHistory(); err != nil {
		m.settings.historyWarning = "Unable to save command history: " + kube.Safe(err.Error())
	}
}

func (m *model) recallCommand(older bool) {
	history := m.settings.CommandHistory
	if len(history) == 0 {
		return
	}
	m.completion = completionState{}
	if older {
		if m.commandHistoryIndex == len(history) {
			m.commandDraft, m.commandDraftCursor = m.input.Value(), m.input.Position()
		}
		if m.commandHistoryIndex == 0 {
			return
		}
		m.commandHistoryIndex--
	} else {
		if m.commandHistoryIndex == len(history) {
			return
		}
		m.commandHistoryIndex++
	}
	if m.commandHistoryIndex == len(history) {
		m.input.SetValue(m.commandDraft)
		m.input.SetCursor(m.commandDraftCursor)
	} else {
		m.input.SetValue(history[m.commandHistoryIndex])
		m.input.CursorEnd()
	}
}
