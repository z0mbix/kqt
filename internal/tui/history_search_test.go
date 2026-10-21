package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"slices"
	"strings"
	"testing"
)

func ctrlHistory(m *model, code rune) { m.key(tea.KeyPressMsg{Code: code, Mod: tea.ModCtrl}) }
func typeHistoryQuery(m *model, text string) {
	for _, code := range text {
		m.key(tea.KeyPressMsg{Code: code, Text: string(code)})
	}
}

func TestReverseHistorySearchCyclesMatchesAndAcceptsWithoutExecuting(t *testing.T) {
	m := demoModel(t)
	m.settings.CommandHistory = []string{"theme nord", "layouts", "theme dracula", "ctx demo"}
	m.startInput("command", "draft")
	originalTheme := m.theme.Name
	ctrlHistory(m, 'r')
	typeHistoryQuery(m, "THEME")
	if m.historySearch.index != 2 || !strings.Contains(ansi.Strip(m.View().Content), "theme dracula") {
		t.Fatal("incremental search did not choose newest case-insensitive match")
	}
	ctrlHistory(m, 'r')
	if m.historySearch.index != 0 {
		t.Fatal("Ctrl+R did not find older match")
	}
	ctrlHistory(m, 'r')
	if !m.historySearch.failed || m.historySearch.index != 0 {
		t.Fatal("exhausted search lost the current match")
	}
	ctrlHistory(m, 's')
	if m.historySearch.failed || m.historySearch.index != 2 {
		t.Fatal("Ctrl+S did not find newer match")
	}
	key(m, "tab")
	if m.mode != "command" || m.input.Value() != "theme dracula" || m.theme.Name != originalTheme || len(m.settings.CommandHistory) != 4 {
		t.Fatal("accepting match executed or changed history")
	}
	key(m, "enter")
	if m.theme.Name != "dracula" || m.settings.CommandHistory[4] != "theme dracula" {
		t.Fatal("accepted command did not execute on Enter")
	}
}

func TestReverseHistoryCancelRestoresDraftAndCursorAndMissingMatchDoesNotRun(t *testing.T) {
	for _, cancel := range []string{"esc", "ctrl+g"} {
		m := demoModel(t)
		m.settings.CommandHistory = []string{"theme nord"}
		before := slices.Clone(m.settings.CommandHistory)
		m.startInput("command", "theme dracula")
		m.input.SetCursor(5)
		ctrlHistory(m, 'r')
		typeHistoryQuery(m, "no matching command")
		key(m, "enter")
		if m.mode != "history-search" || m.historySearch.index != -1 || !slices.Equal(m.settings.CommandHistory, before) {
			t.Fatal("missing match ran or changed history")
		}
		if cancel == "esc" {
			key(m, "esc")
		} else {
			ctrlHistory(m, 'g')
		}
		if m.mode != "command" || m.input.Value() != "theme dracula" || m.input.Position() != 5 || m.input.Prompt != ":" {
			t.Fatal("cancel did not restore draft and cursor")
		}
	}
}

func TestReverseHistoryEnterRunsSelectedCommandAndKeepsRefreshOutsidePrompt(t *testing.T) {
	m := demoModel(t)
	m.settings.CommandHistory = []string{"theme nord", "theme dracula"}
	m.startInput("command", "")
	ctrlHistory(m, 'r')
	typeHistoryQuery(m, "nord")
	key(m, "enter")
	if m.mode != "" || m.theme.Name != "nord" || len(m.settings.CommandHistory) != 3 {
		t.Fatal("Enter did not execute the matching command")
	}
	request := m.request
	ctrlHistory(m, 'r')
	if m.historySearch != nil || m.request <= request {
		t.Fatal("Ctrl+R stopped refreshing outside command prompt")
	}
	m.startInput("filter", "web")
	ctrlHistory(m, 'r')
	if m.historySearch != nil || m.mode != "filter" {
		t.Fatal("history search stole resource-filter keys")
	}
}

func TestReverseHistoryUnicodeBackspaceEmptyHistoryAndResize(t *testing.T) {
	m := demoModel(t)
	m.startInput("command", "draft")
	ctrlHistory(m, 'r')
	ctrlHistory(m, 'r')
	ctrlHistory(m, 's')
	key(m, "enter")
	if m.historySearch.index != -1 {
		t.Fatal("empty history manufactured a command")
	}
	key(m, "esc")
	m.settings.CommandHistory = []string{"download-dir ~/界", "theme nord"}
	ctrlHistory(m, 'r')
	typeHistoryQuery(m, "界x")
	if m.historySearch.index != -1 {
		t.Fatal("missing Unicode query matched")
	}
	m.key(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if m.historySearch.index != 0 {
		t.Fatal("backspace did not rerun incremental search")
	}
	for _, width := range []int{40, 80, 120} {
		m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
		lines := strings.Split(m.View().Content, "\n")
		if len(lines) != 24 {
			t.Fatal("search changed terminal height")
		}
		for _, line := range lines {
			if ansi.StringWidth(line) != width {
				t.Fatal("search overflowed terminal width")
			}
		}
	}
	key(m, "tab")
	if m.input.Value() != "download-dir ~/界" || m.input.Position() != len([]rune(m.input.Value())) {
		t.Fatal("Unicode command was corrupted")
	}
}
